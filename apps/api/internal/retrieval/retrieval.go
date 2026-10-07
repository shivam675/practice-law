// Package retrieval turns stored documents into searchable chunks and
// answers nearest-neighbour queries over them.
//
// Two reasons it exists. The grader needs the passage a quote came from, or
// it cannot verify the quote. The judge needs a question grounded in the
// problem and the authorities within 20 milliseconds, which is a pgvector
// lookup and not a model call.
//
// Search is hybrid: dense and lexical, fused. Dense alone misses a statute
// cited by number, lexical alone misses a paraphrase, and legal argument is
// full of both.
package retrieval

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/intelimek/megamoot/apps/api/internal/harness"
	"github.com/intelimek/megamoot/apps/api/internal/llm"
)

// embedBatch keeps one request under a provider's payload limit without
// turning a 200-chunk memorial into 200 round trips.
const embedBatch = 32

type Store struct {
	pool    *pgxpool.Pool
	harness *harness.Harness
	log     *slog.Logger
}

func NewStore(pool *pgxpool.Pool, h *harness.Harness, log *slog.Logger) *Store {
	return &Store{pool: pool, harness: h, log: log}
}

// Index chunks a parsed document and stores its embeddings.
//
// Idempotent: a document that already has chunks is left alone, which is what
// makes the indexer safe to run on a loop and safe to retry after a provider
// outage.
func (s *Store) Index(ctx context.Context, documentID uuid.UUID) (int, error) {
	var (
		orgID uuid.UUID
		text  string
	)
	err := s.pool.QueryRow(ctx, `
		SELECT organization_id, coalesce(extracted_text, '')
		FROM documents
		WHERE id = $1 AND parse_status = 'parsed'`, documentID).Scan(&orgID, &text)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, fmt.Errorf("document %s is not parsed", documentID)
	}
	if err != nil {
		return 0, fmt.Errorf("load document %s: %w", documentID, err)
	}

	chunks := Split(text)
	if len(chunks) == 0 {
		return 0, nil
	}

	contents := make([]string, len(chunks))
	for i, c := range chunks {
		contents[i] = c.Content
	}

	vectors := make([][]float32, 0, len(chunks))
	for start := 0; start < len(contents); start += embedBatch {
		end := min(start+embedBatch, len(contents))
		batch, err := s.harness.Embed(ctx, harness.Call{
			Purpose:        "index_document",
			OrganizationID: &orgID,
		}, contents[start:end])
		if err != nil {
			return 0, fmt.Errorf("embed document %s: %w", documentID, err)
		}
		vectors = append(vectors, batch...)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin index: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	for i, c := range chunks {
		// ON CONFLICT DO NOTHING rather than an upsert: two indexers racing
		// on the same document should cost one wasted embedding call, not a
		// half-rewritten chunk set.
		if _, err := tx.Exec(ctx, `
			INSERT INTO document_chunks
				(organization_id, document_id, chunk_index, locator, content, token_count, embedding)
			VALUES ($1,$2,$3,$4,$5,$6,$7::vector)
			ON CONFLICT (document_id, chunk_index) DO NOTHING`,
			orgID, documentID, c.Index, c.Locator, c.Content, c.TokenCount,
			llm.Vector(vectors[i])); err != nil {
			return 0, fmt.Errorf("insert chunk %d of %s: %w", c.Index, documentID, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit index: %w", err)
	}
	return len(chunks), nil
}

/* ------------------------------------------------------------------ search */

type Query struct {
	OrganizationID uuid.UUID
	// Text is both halves of the search: embedded for the dense side and
	// parsed into a tsquery for the lexical side.
	Text string
	// Vector skips the embedding call when the caller already has one, which
	// is the live path: the monitor's claim embedding is computed once.
	Vector []float32

	// Scope. At least one must be set, so a query can never walk the whole
	// corpus of an organisation by accident.
	DocumentIDs  []uuid.UUID
	AssessmentID *uuid.UUID
	// SourceKinds filters knowledge_sources.kind, e.g. problem, authority.
	// Only meaningful with AssessmentID.
	SourceKinds []string

	Limit int
}

type Hit struct {
	ChunkID    uuid.UUID
	DocumentID uuid.UUID
	Locator    string
	Content    string
	Score      float64
}

// rrfK is the usual reciprocal-rank-fusion constant. It flattens the
// difference between rank 1 and rank 2 so one confident-but-wrong list cannot
// dominate the other.
const rrfK = 60

// Search fuses a vector search and a full-text search by reciprocal rank.
//
// Scores from the two are not comparable on any scale, which is the whole
// reason to fuse by rank instead of by weighted score: no tuning constant has
// to be guessed, and neither side can be outvoted by the other's units.
func (s *Store) Search(ctx context.Context, q Query) ([]Hit, error) {
	if q.OrganizationID == uuid.Nil {
		return nil, errors.New("retrieval: a query needs an organization_id")
	}
	if len(q.DocumentIDs) == 0 && q.AssessmentID == nil {
		return nil, errors.New("retrieval: a query needs a document or an assessment to search in")
	}
	if q.Limit <= 0 {
		q.Limit = 8
	}

	vector := q.Vector
	if len(vector) == 0 && strings.TrimSpace(q.Text) != "" {
		vectors, err := s.harness.Embed(ctx, harness.Call{
			Purpose:        "retrieval_query",
			OrganizationID: &q.OrganizationID,
		}, []string{q.Text})
		if err != nil {
			return nil, err
		}
		vector = vectors[0]
	}
	if len(vector) == 0 {
		return nil, errors.New("retrieval: a query needs text or a vector")
	}

	// candidates is deliberately wider than Limit: fusion only improves on
	// either list if it can see past each one's top few.
	candidates := q.Limit * 4

	rows, err := s.pool.Query(ctx, `
		WITH scope AS (
			SELECT c.id, c.document_id, c.locator, c.content, c.embedding, c.content_tsv
			FROM document_chunks c
			JOIN documents d ON d.id = c.document_id
			LEFT JOIN knowledge_sources ks ON ks.id = d.knowledge_source_id
			WHERE c.organization_id = $1
			  AND c.embedding IS NOT NULL
			  AND ($2::uuid[] IS NULL OR c.document_id = ANY($2))
			  AND ($3::uuid   IS NULL OR ks.assessment_id = $3)
			  AND ($4::text[] IS NULL OR ks.kind = ANY($4))
		),
		dense AS (
			SELECT id, row_number() OVER (ORDER BY embedding <=> $5::vector) AS rank
			FROM scope ORDER BY embedding <=> $5::vector LIMIT $6
		),
		lexical AS (
			SELECT id, row_number() OVER (
				ORDER BY ts_rank_cd(content_tsv, websearch_to_tsquery('english', $7)) DESC) AS rank
			FROM scope
			WHERE $7 <> '' AND content_tsv @@ websearch_to_tsquery('english', $7)
			ORDER BY ts_rank_cd(content_tsv, websearch_to_tsquery('english', $7)) DESC
			LIMIT $6
		)
		SELECT s.id, s.document_id, s.locator, s.content,
		       coalesce(1.0 / ($8 + d.rank), 0) + coalesce(1.0 / ($8 + l.rank), 0) AS score
		FROM scope s
		LEFT JOIN dense d   ON d.id = s.id
		LEFT JOIN lexical l ON l.id = s.id
		WHERE d.id IS NOT NULL OR l.id IS NOT NULL
		ORDER BY score DESC
		LIMIT $9`,
		q.OrganizationID, nilUUIDs(q.DocumentIDs), q.AssessmentID, nilStrings(q.SourceKinds),
		llm.Vector(vector), candidates, strings.TrimSpace(q.Text), rrfK, q.Limit)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()

	hits := []Hit{}
	for rows.Next() {
		var h Hit
		if err := rows.Scan(&h.ChunkID, &h.DocumentID, &h.Locator, &h.Content, &h.Score); err != nil {
			return nil, fmt.Errorf("scan hit: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// Text returns a document's extracted text. Grading falls back to this when a
// document has no chunks yet, because a missing index must delay a grade, not
// produce a worse one.
func (s *Store) Text(ctx context.Context, orgID, documentID uuid.UUID) (string, error) {
	var text string
	err := s.pool.QueryRow(ctx, `
		SELECT coalesce(extracted_text, '') FROM documents
		WHERE id = $1 AND organization_id = $2`, documentID, orgID).Scan(&text)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("document %s not found", documentID)
	}
	if err != nil {
		return "", fmt.Errorf("load document text: %w", err)
	}
	return text, nil
}

/* ----------------------------------------------------------------- indexer */

// Indexer picks up parsed documents that have no chunks.
//
// A poll rather than a queue, and a goroutine would not survive a restart.
// The work is small, the interval is seconds, and "has no chunks" is a
// question Postgres already has the indexes to answer.
type Indexer struct {
	store    *Store
	log      *slog.Logger
	Interval time.Duration
	Batch    int
}

func NewIndexer(store *Store, log *slog.Logger) *Indexer {
	return &Indexer{store: store, log: log, Interval: 15 * time.Second, Batch: 4}
}

func (ix *Indexer) Run(ctx context.Context) {
	ticker := time.NewTicker(ix.Interval)
	defer ticker.Stop()

	ix.log.Info("document indexer started", "interval", ix.Interval)
	for {
		select {
		case <-ctx.Done():
			ix.log.Info("document indexer stopped")
			return
		case <-ticker.C:
			ix.once(ctx)
		}
	}
}

func (ix *Indexer) once(ctx context.Context) {
	// ponytail: a document whose extracted text is shorter than one chunk is
	// filtered out rather than marked done, so it is re-examined every tick
	// and never indexed. Cheap at this volume; if the corpus grows, this
	// wants an indexed_at column instead of a NOT EXISTS.
	rows, err := ix.store.pool.Query(ctx, `
		SELECT d.id FROM documents d
		WHERE d.parse_status = 'parsed'
		  AND length(coalesce(d.extracted_text, '')) >= 120
		  AND NOT EXISTS (SELECT 1 FROM document_chunks c WHERE c.document_id = d.id)
		ORDER BY d.created_at
		LIMIT $1`, ix.Batch)
	if err != nil {
		ix.log.Error("indexer: find unindexed documents failed", "error", err)
		return
	}

	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			ix.log.Error("indexer: scan failed", "error", err)
			rows.Close()
			return
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		ix.log.Error("indexer: read failed", "error", err)
		return
	}

	for _, id := range ids {
		started := time.Now()
		n, err := ix.store.Index(ctx, id)
		if err != nil {
			// Warn, not error: an unbound embedding tier is the normal state
			// of a fresh install and must not fill the log with stack traces.
			ix.log.Warn("indexer: document not indexed",
				"document_id", id, "error", err)
			// One failure is almost always the provider, so the rest of the
			// batch would fail the same way. Wait for the next tick.
			return
		}
		ix.log.Info("indexed document",
			"document_id", id, "chunks", n, "took", time.Since(started))
	}
}

/* ------------------------------------------------------------------- utils */

// nilUUIDs and nilStrings turn an empty filter into a SQL NULL, which is what
// lets one query carry every optional scope without string concatenation.
func nilUUIDs(v []uuid.UUID) any {
	if len(v) == 0 {
		return nil
	}
	return v
}

func nilStrings(v []string) any {
	if len(v) == 0 {
		return nil
	}
	return v
}
