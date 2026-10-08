package submissions

import (
 "bytes"
 "context"
 "crypto/sha256"
 "errors"
 "fmt"
 "io"
 "net/http"
 "path"
 "strings"

 "github.com/go-chi/chi/v5"
 "github.com/google/uuid"
 "github.com/jackc/pgx/v5"
 "github.com/intelimek/megamoot/apps/api/internal/aiclient"
 "github.com/intelimek/megamoot/apps/api/internal/audit"
 "github.com/intelimek/megamoot/apps/api/internal/auth"
 "github.com/intelimek/megamoot/apps/api/internal/httpx"
 "github.com/intelimek/megamoot/apps/api/internal/spec"
)

type Resource struct {
 ID uuid.UUID `json:"id"`
 Title string `json:"title"`
 Kind string `json:"kind"`
 Visibility string `json:"visibility"`
}

func (s *Store) resourceParticipation(ctx context.Context, orgID, assessmentID uuid.UUID) (spec.Participation, error) {
 var raw []byte
 err := s.pool.QueryRow(ctx, `SELECT v.participation FROM assessments a JOIN assessment_template_versions v ON v.id=a.template_version_id JOIN assessment_templates t ON t.id=v.template_id AND t.organization_id=a.organization_id WHERE a.id=$1 AND a.organization_id=$2`, assessmentID, orgID).Scan(&raw)
 if errors.Is(err, pgx.ErrNoRows) { return spec.Participation{}, httpx.ErrNotFound() }
 if err != nil { return spec.Participation{}, fmt.Errorf("load resource assessment: %w", err) }
 return spec.DecodeParticipation(raw)
}

func validateResource(title, kind, visibility string, participation spec.Participation) error {
 if strings.TrimSpace(title)=="" || len(title)>300 { return httpx.ErrBadRequest("A title of up to 300 characters is required.") }
 switch kind { case "problem", "authority", "statute", "evidence", "guidance", "other": default: return httpx.ErrBadRequest("Choose a supported material kind.") }
 if visibility!="all" && visibility!="staff" && !participation.HasSide(visibility) { return httpx.ErrBadRequest("Choose who can see this material.") }
 return nil
}

func (h *Handlers) ListResources(w http.ResponseWriter, r *http.Request) {
 p:=auth.MustPrincipal(r.Context())
 if !p.Can("knowledge.view") { httpx.Fail(w,r,httpx.ErrForbidden()); return }
 id,err:=uuid.Parse(chi.URLParam(r,"assessmentID")); if err!=nil { httpx.Fail(w,r,httpx.ErrBadRequest("Invalid assessment id.")); return }
 if _,err=h.store.resourceParticipation(r.Context(),p.OrganizationID,id); err!=nil { httpx.Fail(w,r,err); return }
 // This teacher endpoint never returns document contents. Students use the assignment endpoint, which applies stage and side visibility.
 if !p.Can("assessment.view") { httpx.Fail(w,r,httpx.ErrForbidden()); return }
 rows,err:=h.store.pool.Query(r.Context(),`SELECT id,title,kind,visibility FROM knowledge_sources WHERE assessment_id=$1 AND organization_id=$2 ORDER BY created_at,id`,id,p.OrganizationID)
 if err!=nil { httpx.Fail(w,r,fmt.Errorf("list materials: %w",err)); return }; defer rows.Close()
 out:=[]Resource{}
 for rows.Next() { var item Resource; if err=rows.Scan(&item.ID,&item.Title,&item.Kind,&item.Visibility); err!=nil { httpx.Fail(w,r,err); return }; out=append(out,item) }
 if err=rows.Err(); err!=nil { httpx.Fail(w,r,err); return }
 httpx.JSON(w,r,http.StatusOK,map[string]any{"resources":out})
}

func (h *Handlers) UploadResource(w http.ResponseWriter, r *http.Request) {
 p:=auth.MustPrincipal(r.Context())
 if !p.Can("knowledge.upload") { httpx.Fail(w,r,httpx.ErrForbidden()); return }
 id,err:=uuid.Parse(chi.URLParam(r,"assessmentID")); if err!=nil { httpx.Fail(w,r,httpx.ErrBadRequest("Invalid assessment id.")); return }
 participation,err:=h.store.resourceParticipation(r.Context(),p.OrganizationID,id); if err!=nil { httpx.Fail(w,r,err); return }
 const limit=25<<20
 r.Body=http.MaxBytesReader(w,r.Body,limit+(1<<20))
 if err=r.ParseMultipartForm(1<<20); err!=nil { httpx.Fail(w,r,httpx.ErrBadRequest("Upload a file up to 25 MB.")); return }
 defer r.MultipartForm.RemoveAll()
 out:=Resource{ID:uuid.New(),Title:strings.TrimSpace(r.FormValue("title")),Kind:r.FormValue("kind"),Visibility:r.FormValue("visibility")}
 if err=validateResource(out.Title,out.Kind,out.Visibility,participation); err!=nil { httpx.Fail(w,r,err); return }
 file,header,err:=r.FormFile("file"); if err!=nil { httpx.Fail(w,r,httpx.ErrBadRequest("A file is required.")); return }; defer file.Close()
 data,err:=io.ReadAll(io.LimitReader(file,limit+1)); if err!=nil { httpx.Fail(w,r,fmt.Errorf("read material: %w",err)); return }
 if len(data)>limit { httpx.Fail(w,r,httpx.Err(http.StatusRequestEntityTooLarge,"file_too_large","The file must be at most 25 MB.")); return }
 format,err:=sniff(data); if err!=nil { httpx.Fail(w,r,httpx.ErrBadRequest("Upload a PDF, Word or text document.")); return }
 filename:=safeFilename(header.Filename)
 extraction,err:=h.store.ai.Extract(r.Context(),filename,data)
 if err!=nil { var e *aiclient.ExtractError; if errors.As(err,&e)&&e.Unsupported() { err=httpx.ErrBadRequest("This document could not be read. Upload a text document or searchable PDF.") }; httpx.Fail(w,r,err); return }
 if extraction.Truncated { httpx.Fail(w,r,httpx.ErrBadRequest("This document is too long to read completely. Split it into smaller files.")); return }
 documentID:=uuid.New(); key:=path.Join("org",p.OrganizationID.String(),"assessment",id.String(),documentID.String()+"."+format)
 if err=h.store.blobs.Put(r.Context(),key,bytes.NewReader(data),int64(len(data)),contentTypeFor(format)); err!=nil { httpx.Fail(w,r,fmt.Errorf("store material: %w",err)); return }
 tx,err:=h.store.pool.Begin(r.Context()); if err!=nil { httpx.Fail(w,r,err); return }; defer tx.Rollback(context.WithoutCancel(r.Context()))
 _,err=tx.Exec(r.Context(),`INSERT INTO knowledge_sources(id,organization_id,assessment_id,kind,title,visibility,created_by) VALUES($1,$2,$3,$4,$5,$6,$7)`,out.ID,p.OrganizationID,id,out.Kind,out.Title,out.Visibility,p.UserID)
 if err!=nil { httpx.Fail(w,r,fmt.Errorf("record material: %w",err)); return }
 digest:=sha256.Sum256(data)
 _,err=tx.Exec(r.Context(),`INSERT INTO documents(id,organization_id,knowledge_source_id,storage_key,filename,content_type,byte_size,sha256,parse_status,extracted_text,page_count) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'parsed',$9,$10)`,documentID,p.OrganizationID,out.ID,key,filename,contentTypeFor(format),len(data),digest[:],extraction.Text,extraction.Pages)
 if err!=nil { httpx.Fail(w,r,fmt.Errorf("record material document: %w",err)); return }
 if err=tx.Commit(r.Context()); err!=nil { httpx.Fail(w,r,err); return }
 h.audit.Record(r.Context(),audit.Entry{OrganizationID:&p.OrganizationID,ActorUserID:&p.UserID,Action:"knowledge.upload",TargetKind:"knowledge_source",TargetID:&out.ID,After:map[string]any{"assessment_id":id,"kind":out.Kind,"visibility":out.Visibility,"bytes":len(data)},RequestID:httpx.RequestIDFrom(r.Context())})
 httpx.JSON(w,r,http.StatusCreated,out)
}
