package api

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/drewlesueur/tts-inventory-getter/internal/config"
	"github.com/drewlesueur/tts-inventory-getter/internal/model"
	"github.com/drewlesueur/tts-inventory-getter/internal/scrape"
	"github.com/drewlesueur/tts-inventory-getter/internal/store"
	"go.uber.org/zap"
)

func TestNHTSAAPI(t *testing.T) {
	ctx := context.Background()
	st, err := store.NewSQLiteResultStore(filepath.Join(t.TempDir(), "jobs.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err = st.InitNHTSA(ctx); err != nil {
		t.Fatal(err)
	}
	s := NewServer(config.Config{ServiceKey: "key", RequestBodyLimitMB: 1, RateLimitRPS: 1000, RateLimitBurst: 1000}, zap.NewNop(), scrape.Service{}, config.Loader{}, st, nil, nil, nil, nil)
	router := s.Router()
	request := func(method, path, body, dealer, key string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("X-Service-Key", key)
		r.Header.Set("X-Dealership-Id", dealer)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	body := `{"vin":"1FMUK7DHXSGA27345","dealershipId":"dealer-a","idempotencyKey":"same"}`
	if w := request("POST", "/v1/nhtsa/lookups", body, "", "bad"); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := request("POST", "/v1/nhtsa/lookups", strings.Replace(body, "1FMUK7DHXSGA27345", "INVALID", 1), "", "key"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := request("POST", "/v1/nhtsa/lookups", body, "", "key")
	if w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
	var job model.NHTSALookup
	json.Unmarshal(w.Body.Bytes(), &job)
	if !nhtsaID.MatchString(job.ResultID) || job.Status != "pending" {
		t.Fatal(job)
	}
	duplicate := request("POST", "/v1/nhtsa/lookups", body, "", "key")
	if duplicate.Body.String() != w.Body.String() {
		t.Fatal("duplicate created another job")
	}
	if w := request("POST", "/v1/nhtsa/lookups", strings.Replace(body, "1FMUK7DHXSGA27345", "1FMCU9GDXJUD24965", 1), "", "key"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	other := request("POST", "/v1/nhtsa/lookups", strings.Replace(body, "dealer-a", "dealer-b", 1), "", "key")
	if other.Code != 202 || other.Body.String() == w.Body.String() {
		t.Fatal("dealership keys not independent")
	}
	path := "/v1/nhtsa/lookups/" + job.ResultID
	if w := request("GET", path, "", "dealer-b", "key"); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w := request("GET", path, "", "", "key"); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w := request("GET", path+"/pdf", "", "dealer-a", "key"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	claimed, err := st.ClaimNHTSA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.LookupDate == nil || claimed.Status != "running" {
		t.Fatal(claimed)
	}
	if _, err := st.ClaimNHTSA(ctx); err != store.ErrNotFound {
		t.Fatal("unbounded concurrency", err)
	}
	pdf := []byte("%PDF-1.7\nfixture")
	if err := st.FinishNHTSA(ctx, claimed.ResultID, pdf, nil); err != nil {
		t.Fatal(err)
	}
	completedPath := "/v1/nhtsa/lookups/" + claimed.ResultID
	w = request("GET", completedPath+"/pdf", "", claimed.DealershipID, "key")
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/pdf" || w.Body.String() != string(pdf) {
		t.Fatal(w.Code, w.Body.String())
	}
	next, err := st.ClaimNHTSA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s.runNHTSA(ctx, st, failingNHTSABrowser{}, next)
	failed, err := st.GetNHTSA(ctx, next.ResultID, next.DealershipID)
	if err != nil || failed.Status != "failed" || failed.Error.Code != "ACCESS_BLOCKED" {
		t.Fatal(failed, err)
	}
	if w := request("GET", "/v1/nhtsa/lookups/"+next.ResultID+"/pdf", "", next.DealershipID, "key"); w.Code != 409 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/v1/nhtsa/lookups/"+strings.Repeat("x", 161), "", "dealer-a", "key"); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

type failingNHTSABrowser struct{}

func (failingNHTSABrowser) NHTSALookup(context.Context, string, time.Time) ([]byte, *model.NHTSAError) {
	return nil, &model.NHTSAError{Code: "ACCESS_BLOCKED", Message: "NHTSA denied access"}
}
