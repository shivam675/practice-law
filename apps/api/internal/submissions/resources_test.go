package submissions
import (
 "testing"
 "github.com/intelimek/megamoot/apps/api/internal/spec"
)
func TestResourceValidation(t *testing.T) {
 p:=spec.Participation{Sides:[]string{"candidate"}}
 for _,v:=range []string{"all","staff","candidate"} { if err:=validateResource("Case","problem",v,p);err!=nil { t.Fatal(err) } }
 for _,v:=range []struct{title,kind,visibility string}{{"","problem","all"},{"Case","unknown","all"},{"Case","problem","respondent"}} { if validateResource(v.title,v.kind,v.visibility,p)==nil {t.Fatalf("accepted invalid material: %+v",v)} }
}
