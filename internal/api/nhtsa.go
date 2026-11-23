package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
	"go.uber.org/zap"
)

type nhtsaStore interface {
	InitNHTSA(context.Context) error
	CreateNHTSA(context.Context, string, string, string) (model.NHTSALookup, error)
	GetNHTSA(context.Context, string, string) (model.NHTSALookup, error)
	NHTSAPDF(context.Context, string, string) ([]byte, error)
	ClaimNHTSA(context.Context) (model.NHTSALookup, error)
	FinishNHTSA(context.Context, string, []byte, *model.NHTSAError) error
	MaintainNHTSA(context.Context) error
}
type nhtsaBrowser interface {
	NHTSALookup(context.Context, string, time.Time) ([]byte, *model.NHTSAError)
}

var nhtsaVIN = regexp.MustCompile(`^[A-HJ-NPR-Z0-9]{17}$`)
var nhtsaID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,160}$`)

// StartNHTSA attaches a durable job loop to the existing server worker lifecycle.
// Database claims enforce one active lookup across instances. Pending jobs survive restart.
func (s *Server) StartNHTSA(ctx context.Context) (func(), error) {
	st, ok := s.store.(nhtsaStore)
	if !ok {
		return nil, errors.New("NHTSA requires SQLite job storage")
	}
	browser, ok := s.scraper.Browser.(nhtsaBrowser)
	if !ok {
		return nil, errors.New("NHTSA requires Playwright browser")
	}
	if err := st.InitNHTSA(ctx); err != nil {
		return nil, err
	}
	workerCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			if workerCtx.Err() != nil {
				return
			}
			if err := st.MaintainNHTSA(workerCtx); err != nil {
				s.logger.Error("NHTSA maintenance failed", zap.Error(err))
			}
			job, err := st.ClaimNHTSA(workerCtx)
			if err == nil {
				s.runNHTSA(workerCtx, st, browser, job)
				continue
			}
			if !errors.Is(err, store.ErrNotFound) && workerCtx.Err() == nil {
				s.logger.Error("NHTSA claim failed", zap.Error(err))
			}
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return func() { cancel(); <-done }, nil
}
func (s *Server) runNHTSA(ctx context.Context, st nhtsaStore, browser nhtsaBrowser, job model.NHTSALookup) {
	var pdf []byte
	var failure *model.NHTSAError
	defer func() {
		if recover() != nil {
			failure = &model.NHTSAError{Code: "LOOKUP_FAILED", Message: "Browser worker interrupted"}
		}
		persist, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := st.FinishNHTSA(persist, job.ResultID, pdf, failure); err != nil {
			s.logger.Error("NHTSA completion persistence failed", zap.String("resultId", job.ResultID), zap.Error(err))
		}
	}()
	run, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()
	pdf, failure = browser.NHTSALookup(run, job.VIN, *job.LookupDate)
}
func (s *Server) handleNHTSACreate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VIN            string `json:"vin"`
		DealershipID   string `json:"dealershipId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeJSON(w, 400, model.ErrorResponse("INVALID_REQUEST", err.Error()))
		return
	}
	req.VIN = strings.ToUpper(strings.TrimSpace(req.VIN))
	req.DealershipID = strings.TrimSpace(req.DealershipID)
	req.IdempotencyKey = strings.TrimSpace(req.IdempotencyKey)
	if !nhtsaVIN.MatchString(req.VIN) {
		writeJSON(w, 400, model.ErrorResponse("INVALID_VIN", "VIN must contain 17 letters/digits excluding I, O, and Q"))
		return
	}
	if len(req.DealershipID) == 0 || len(req.DealershipID) > 160 || len(req.IdempotencyKey) == 0 || len(req.IdempotencyKey) > 160 {
		writeJSON(w, 400, model.ErrorResponse("INVALID_REQUEST", "dealershipId and idempotencyKey must contain 1–160 bytes"))
		return
	}
	if dealer := r.Header.Get("X-Dealership-Id"); dealer != "" && dealer != req.DealershipID {
		writeJSON(w, 400, model.ErrorResponse("DEALERSHIP_MISMATCH", "header and body dealership differ"))
		return
	}
	st, ok := s.store.(nhtsaStore)
	if !ok {
		writeJSON(w, 503, model.ErrorResponse("UNAVAILABLE", "lookup storage unavailable"))
		return
	}
	job, err := st.CreateNHTSA(r.Context(), req.DealershipID, req.VIN, req.IdempotencyKey)
	switch {
	case errors.Is(err, store.ErrNHTSAConflict):
		writeJSON(w, 409, model.ErrorResponse("IDEMPOTENCY_CONFLICT", err.Error()))
	case errors.Is(err, store.ErrNHTSAQueueFull):
		writeJSON(w, 503, model.ErrorResponse("QUEUE_FULL", err.Error()))
	case err != nil:
		writeJSON(w, 500, model.ErrorResponse("STORE_FAILED", "could not persist lookup"))
	default:
		writeJSON(w, http.StatusAccepted, map[string]string{"resultId": job.ResultID, "status": job.Status})
	}
}
func (s *Server) handleNHTSAGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("resultId")
	dealer := strings.TrimSpace(r.Header.Get("X-Dealership-Id"))
	if !nhtsaID.MatchString(id) {
		writeJSON(w, 400, model.ErrorResponse("INVALID_RESULT_ID", "invalid result ID"))
		return
	}
	if dealer == "" || len(dealer) > 160 {
		writeJSON(w, 400, model.ErrorResponse("DEALERSHIP_REQUIRED", "X-Dealership-Id is required"))
		return
	}
	st, ok := s.store.(nhtsaStore)
	if !ok {
		writeJSON(w, 503, model.ErrorResponse("UNAVAILABLE", "lookup storage unavailable"))
		return
	}
	job, err := st.GetNHTSA(r.Context(), id, dealer)
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, 404, model.ErrorResponse("NOT_FOUND", "lookup not found for dealership"))
		return
	}
	if err != nil {
		writeJSON(w, 500, model.ErrorResponse("STORE_FAILED", "could not read lookup"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasSuffix(r.URL.Path, "/pdf") {
		writeJSON(w, 200, job)
		return
	}
	if job.Status != "completed" {
		writeJSON(w, 409, model.ErrorResponse("PDF_NOT_READY", "lookup status is "+job.Status))
		return
	}
	pdf, err := st.NHTSAPDF(r.Context(), id, dealer)
	if err != nil {
		writeJSON(w, 500, model.ErrorResponse("PDF_UNAVAILABLE", "could not read PDF"))
		return
	}
	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Content-Disposition", `attachment; filename="nhtsa-`+id+`.pdf"`)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(200)
	_, _ = w.Write(pdf)
}
