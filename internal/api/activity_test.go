package api

import (
    "bytes"
    "fmt"
    "context"
    "encoding/json"
    "errors"
    "net/http"
    "net/http/httptest"
    "testing"
    "time"
    "github.com/drewlesueur/tts-inventory-getter/internal/config"
    "github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
    "github.com/drewlesueur/tts-inventory-getter/internal/scrape"
    "github.com/drewlesueur/tts-inventory-getter/internal/store"
    "go.uber.org/zap"
)

func TestInventoryActivityManualFailureAndCache(t *testing.T) {
    var events []inventoryapi.Activity
    receiver:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        if r.URL.Path!="/api/inventory/activity" || r.Header.Get("X-Service-Key")!="fixture" {t.Error("incorrect report target or key")}
        var body struct { Event inventoryapi.Activity `json:"event"` }
        if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil {t.Error(err)}
        events=append(events,body.Event);w.WriteHeader(200)
    }));defer receiver.Close()
    client:=&inventoryapi.Client{BaseURL:receiver.URL,ServiceKey:"fixture"}
    st:=store.NewMemoryResultStore()
    s:=NewServer(config.Config{ScrapeMaxAttempts:1},zap.NewNop(),scrape.Service{Fetcher:alwaysErrFetcher{err:errors.New("fixture unavailable")},Extractors:[]scrape.Extractor{scrape.DOMExtractor{}},Concurrency:1},config.Loader{},st,testMetrics(),nil,client,nil)
    s.runScrapeAsync("manual-result","dealer","https://fixture.invalid/inventory","key",testSiteConfig(),time.Second,nil)
    if len(events)!=1 || events[0].Status!="failed" || events[0].Trigger!="manual" || events[0].Phase!="scrape" || events[0].ID!="manual-result" {t.Fatalf("bad failure event: %+v",events)}
    // Returning existing cached vehicles must not be presented as a live success.
    source:="https://www.jjsadobeauto.com/cars-for-sale"
    if err:=st.UpsertCachedInventory(context.Background(),store.CachedInventory{SourceURL:source,Items:[]model.InventoryItem{{StockID:"one"}},UpdatedAt:time.Now().Add(-48*time.Hour)});err!=nil{t.Fatal(err)}
    if !s.serveCacheOnlyScrapeOnce(httptest.NewRecorder(),httptest.NewRequest("POST","/",nil),"dealer",source,"cached-key","signup"){t.Fatal("cache branch not taken")}
    if len(events)!=2 || events[1].Status!="cached" || events[1].ItemCount!=1 || events[1].Trigger!="signup" {t.Fatalf("bad cached event: %+v",events)}
    // An empty cache is not evidence that the dealership scrape is working.
    if err:=st.UpsertCachedInventory(context.Background(),store.CachedInventory{SourceURL:source,UpdatedAt:time.Now()});err!=nil {t.Fatal(err)}
    if !s.serveCacheOnlyScrapeOnce(httptest.NewRecorder(),httptest.NewRequest("POST","/",nil),"dealer",source,"empty-key","scheduled"){t.Fatal("empty cache branch not taken")}
    if len(events)!=3 || events[2].Status!="failed" || events[2].Trigger!="scheduled" {t.Fatalf("bad empty cache event: %+v",events)}
}

// Initialization failures must be visible even though no scrape worker starts.
type activityInitializationFailureStore struct {
    store.ResultStore
    failWrite bool
}

func (s activityInitializationFailureStore) DeleteIdempotency(ctx context.Context, key string) error {
    if !s.failWrite { return errors.New("fixture preparation failed") }
    return s.ResultStore.DeleteIdempotency(ctx, key)
}

func (s activityInitializationFailureStore) UpsertResult(ctx context.Context, result model.ScrapeResult) error {
    if s.failWrite { return errors.New("fixture result write failed") }
    return s.ResultStore.UpsertResult(ctx, result)
}

func TestInventoryActivityInitializationFailures(t *testing.T) {
    for _,failWrite:=range []bool{false,true} {
        for _,path:=range []string{"/v1/scrape/once", "/v1/scrape/once-and-result"} {
            t.Run(fmt.Sprintf("%s/write=%t",path,failWrite),func(t *testing.T){
                reports:=make(chan inventoryapi.Activity,2)
                receiver:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
                    var body struct { Event inventoryapi.Activity `json:"event"` }
                    if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil {t.Error(err)}
                    reports<-body.Event
                }));defer receiver.Close()
                st:=activityInitializationFailureStore{ResultStore:store.NewMemoryResultStore(),failWrite:failWrite}
                s:=NewServer(config.Config{},zap.NewNop(),scrape.Service{},config.Loader{},st,testMetrics(),nil,&inventoryapi.Client{BaseURL:receiver.URL},nil)
                site:=testSiteConfig()
                body,_:=json.Marshal(ScrapeOnceRequest{DealershipID:"dealer",SourceURL:"https://fixture.invalid/inventory",IdempotencyKey:"request-key",SiteConfig:&site,Options:&ScrapeOptions{ActivityTrigger:"signup"}})
                request:=httptest.NewRequest("POST",path,bytes.NewReader(body))
                response:=httptest.NewRecorder()
                if path=="/v1/scrape/once" {s.handleScrapeOnce(response,request)} else {s.handleScrapeOnceAndResult(response,request)}
                if response.Code!=500 {t.Fatalf("response=%d %s",response.Code,response.Body.String())}
                select {
                case event:=<-reports:
                    if event.Status!="failed" || event.Trigger!="signup" {t.Fatalf("incorrect initialization outcome: %+v",event)}
                default: t.Fatal("missing initialization failure report")
                }
            })
        }
    }
}
