package api

import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
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

func TestDiagnosticsAsyncSQLiteRetries(t *testing.T) {
    st,err:=store.NewSQLiteResultStore(filepath.Join(t.TempDir(),"retry.sqlite"));if err!=nil {t.Fatal(err)};defer st.Close()
    svc:=scrape.Service{Fetcher:&seqFetcher{},Extractors:[]scrape.Extractor{scrape.DOMExtractor{}},Concurrency:1}
    s:=NewServer(config.Config{ScrapeMaxAttempts:2,ScrapeRetryBackoffSec:1},zap.NewNop(),svc,config.Loader{},st,testMetrics(),nil,nil,nil)
    s.runScrapeAsync("retry-result","dealer","https://dealer.test/inventory","retry-key",testSiteConfig(),time.Second,nil)
    result,err:=st.GetResult(context.Background(),"retry-result");if err!=nil {t.Fatal(err)}
    if result.Status!=model.RunStatusSuccess || result.AttemptCount!=2 || len(result.Items)!=1 {t.Fatalf("unexpected recovery: %+v",result)}
    attempts:=map[int]bool{};sawRetry:=false
    for _,e:=range result.Diagnostics.Events {if e.Stage=="attempt_started" {attempts[e.Attempt]=true};if e.Stage=="retry_wait" {sawRetry=true}}
    if !attempts[1] || !attempts[2] || !sawRetry {t.Fatalf("lost retry trail: %+v",result.Diagnostics)}
    response:=resultResponse(result);if response["diagnostics"]==nil {t.Fatal("result response omits diagnostics")}
}

func TestDiagnosticsPreflightAndStoreFailures(t *testing.T) {
    for _,failWrite:=range []bool{false,true} {
        st:=activityInitializationFailureStore{ResultStore:store.NewMemoryResultStore(),failWrite:failWrite}
        s:=NewServer(config.Config{},zap.NewNop(),scrape.Service{},config.Loader{},st,testMetrics(),nil,nil,nil)
        site:=testSiteConfig()
        body,_:=json.Marshal(ScrapeOnceRequest{DealershipID:"dealer",SourceURL:"https://fixture.invalid/inventory",SiteConfig:&site})
        response:=httptest.NewRecorder()
        s.handleScrapeOnce(response,httptest.NewRequest("POST","/",bytes.NewReader(body)))
        var payload struct { Diagnostics *model.Diagnostics `json:"diagnostics"`;ResultID string `json:"resultId"` }
        if err:=json.Unmarshal(response.Body.Bytes(),&payload);err!=nil {t.Fatal(err)}
        if response.Code!=500 || payload.Diagnostics==nil || len(payload.Diagnostics.Events)==0 {t.Fatalf("missing error diagnostics: %s",response.Body.String())}
        if !failWrite {
            if payload.ResultID=="" {t.Fatal("missing persisted failure ID")}
            stored,err:=st.GetResult(context.Background(),payload.ResultID);if err!=nil || stored.Diagnostics==nil || stored.Status!=model.RunStatusFailed {t.Fatal(stored,err)}
        }
    }
    ctx,d:=scrape.WithDiagnostics(context.Background());_ = ctx
    response:=diagnosticErrorResponse("FIXTURE",errors.New(`Authorization: Bearer fixture-secret`),d)
    b,_:=json.Marshal(response);if strings.Contains(string(b),"fixture-secret") {t.Fatal("preflight secret retained")}
}

func TestDiagnosticsSyncResultAndCache(t *testing.T) {
    for _,cache:=range []bool{false,true} {
        st,err:=store.NewSQLiteResultStore(filepath.Join(t.TempDir(),"sync.sqlite"));if err!=nil {t.Fatal(err)}
        func(){defer st.Close()
            source:="https://fixture.invalid/inventory"
            cfg:=config.Config{}
            var svc scrape.Service
            if cache {
                cfg.CacheOnlyURLs=[]string{source}
                if err:=st.UpsertCachedInventory(context.Background(),store.CachedInventory{SourceURL:source,Items:[]model.InventoryItem{{StockID:"CACHED"}},UpdatedAt:time.Now().Add(-24*time.Hour)});err!=nil {t.Fatal(err)}
            } else {
                svc=scrape.Service{Fetcher:alwaysErrFetcher{err:errors.New("fixture fetch failed")},Extractors:[]scrape.Extractor{scrape.DOMExtractor{}},Concurrency:1}
            }
            s:=NewServer(cfg,zap.NewNop(),svc,config.Loader{},st,testMetrics(),nil,nil,nil)
            body:=`{"dealershipId":"dealer","url":"`+source+`"}`
            response:=httptest.NewRecorder();s.handleScrapeRun(response,httptest.NewRequest("POST","/",strings.NewReader(body)))
            var payload struct { ResultID string `json:"resultId"`;Diagnostics *model.Diagnostics `json:"diagnostics"` }
            if err:=json.Unmarshal(response.Body.Bytes(),&payload);err!=nil {t.Fatal(err)}
            if payload.ResultID=="" || payload.Diagnostics==nil {t.Fatalf("missing sync evidence: %s",response.Body.String())}
            record,err:=st.GetResult(context.Background(),payload.ResultID);if err!=nil {t.Fatal(err)}
            if cache && (len(record.Items)!=1 || record.Items[0].StockID!="CACHED") {t.Fatal("cached result ID loses inventory")}
            if !cache && record.Status!=model.RunStatusFailed {t.Fatalf("bad preflight status: %+v",record)}
        }()
    }
}
