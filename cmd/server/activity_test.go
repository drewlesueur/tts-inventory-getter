package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "net/http/httptest"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/drewlesueur/tts-inventory-getter/internal/config"
    "github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
    "github.com/drewlesueur/tts-inventory-getter/internal/scrape"
    "github.com/drewlesueur/tts-inventory-getter/internal/sites"
    "github.com/drewlesueur/tts-inventory-getter/internal/store"
    "go.uber.org/zap"
)

func TestInventoryActivityScheduledCacheAndFailure(t *testing.T) {
    for _,hasCache:=range []bool{false,true} {
        t.Run(map[bool]string{false:"missing cache",true:"cached import source"}[hasCache],func(t *testing.T){
            var events []inventoryapi.Activity
            receiver:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
                if r.URL.Path!="/api/inventory/activity" {t.Errorf("unexpected network request %s",r.URL.Path)}
                var body struct { Event inventoryapi.Activity `json:"event"` }
                if err:=json.NewDecoder(r.Body).Decode(&body);err!=nil {t.Error(err)}
                events=append(events,body.Event)
            }));defer receiver.Close()
            client:=&inventoryapi.Client{BaseURL:receiver.URL}
            source:="https://fixture.invalid/inventory"
            st:=store.NewMemoryResultStore()
            if hasCache {if err:=st.UpsertCachedInventory(context.Background(),store.CachedInventory{SourceURL:source,Items:[]model.InventoryItem{{StockID:"one"}},UpdatedAt:time.Now().Add(-24*time.Hour)});err!=nil {t.Fatal(err)}}
            pages:=[]inventoryapi.PageEntry{{AccountID:"a1",DealershipID:"d1",URL:source}}
            result:=scrapeAllPages(zap.NewNop(),config.Config{CacheOnlyURLs:[]string{source}},scrape.Service{},config.Loader{},nil,st,client,"test","daily",pages)
            expected:="failed";if hasCache {expected="cached"}
            if len(events)!=1 || events[0].Status!=expected || events[0].Trigger!="scheduled" || events[0].Phase!="scrape" {t.Fatalf("events=%+v",events)}
            if (len(result)==1)!=hasCache {t.Fatalf("scrape behavior changed: %+v",result)}
        })
    }
}

type stockFixtureTransport func(*http.Request) (*http.Response, error)

func (f stockFixtureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestScheduledInventoryReportsStockAssignment(t *testing.T) {
	const source = "https://stock-fixture.invalid/new-vehicles/"
	const firstVIN = "4S4GUHD68T3726848"
	const secondVIN = "4S4SLDA61T3152538"
	cfg := config.Config{DefaultRunTimeoutSec: 15}
	loader := config.NewLoader(t.TempDir())
	key := sites.CacheKeyForSourceURL(source)
	if err := loader.SaveByName(key, config.SiteConfig{Name: key, BaseURL: source,
		CarsCommerce: &config.CarsCommerceConfig{CCID: "1234", SearchKey: "fixture", VehicleType: "New", SkipUnstockedInTransit: true},
	}); err != nil {
		t.Fatal(err)
	}
	st := store.NewMemoryResultStore()
	ctx := context.Background()
	if err := st.FlagProtectedURL(ctx, store.ProtectedURL{SourceURL: source}); err != nil {
		t.Fatal(err)
	}
	if err := st.UpsertCachedInventory(ctx, store.CachedInventory{SourceURL: source, Items: []model.InventoryItem{{StockID: "stale-stock"}}}); err != nil {
		t.Fatal(err)
	}

	var received struct {
		sync.Mutex
		scrapeEvent inventoryapi.Activity
		imported    []model.InventoryItem
	}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		received.Lock()
		defer received.Unlock()
		switch req.URL.Path {
		case "/api/inventory/activity":
			var input struct{ Event inventoryapi.Activity }
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.Event.Phase == "scrape" {
				received.scrapeEvent = input.Event
			}
		case "/upsertAccountInventory":
			var input struct {
				Items             []model.InventoryItem
				ActivitySourceURL string
			}
			if err := json.NewDecoder(req.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			if input.ActivitySourceURL != source {
				t.Errorf("wrong import source: %s", input.ActivitySourceURL)
			}
			received.imported = input.Items
			fmt.Fprint(w, `{"status":200,"data":{"created":1}}`)
		default:
			t.Errorf("unexpected path: %s", req.URL.Path)
			http.NotFound(w, req)
		}
	}))
	defer receiver.Close()

	var firstStock, secondStock string
	providerCalls := 0
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = stockFixtureTransport(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host != "websites-search.api.carscommerce.inc" {
			return transport.RoundTrip(req)
		}
		providerCalls++
		listing := func(vin, stock string) string {
			return fmt.Sprintf(`{"vin":%q,"stock":%q,"type":"New","in_transit":"yes","vdp_url":%q}`, vin, stock, "/inventory/new-"+vin)
		}
		body := fmt.Sprintf(`{"data":{"listings":[%s,%s]},"meta":{"pagination":{"current_page":1,"total_pages":1,"total":2,"count":2}}}`,
			listing(firstVIN, firstStock), listing(secondVIN, secondStock))
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	client := &inventoryapi.Client{BaseURL: receiver.URL}
	pages := []inventoryapi.PageEntry{{AccountID: "fixture-account", DealershipID: "fixture-dealer", URL: source}}
	run := func() (inventoryapi.Activity, []model.InventoryItem) {
		received.Lock()
		received.imported = nil
		received.Unlock()
		runScheduledUpsertWithPages(zap.NewNop(), cfg, scrape.Service{}, loader, nil, st, client, "test", "daily", pages)
		received.Lock()
		defer received.Unlock()
		return received.scrapeEvent, received.imported
	}
	cfg.CacheOnlyURLs = []string{source}
	scrapeEvent, imported := run()
	if providerCalls != 0 || len(imported) != 0 || !strings.Contains(scrapeEvent.Message, "Cache-only configuration") {
		t.Fatalf("cache-only configuration result: provider calls=%d, imported=%v, event=%+v", providerCalls, imported, scrapeEvent)
	}
	cfg.CacheOnlyURLs = nil
	scrapeEvent, imported = run()
	failed, err := st.GetResult(ctx, scrapeEvent.ID)
	if err != nil || failed.Status != model.RunStatusFailed || len(imported) != 0 ||
		!strings.Contains(scrapeEvent.Message, "2 in-transit vehicles excluded") {
		t.Fatalf("all-excluded result: record=%+v, event=%+v, import=%v, err=%v", failed, scrapeEvent, imported, err)
	}
	firstStock = "6N5625"
	scrapeEvent, imported = run()
	if len(imported) != 1 || imported[0].VIN != firstVIN || scrapeEvent.Status != "partial_success" ||
		!strings.Contains(scrapeEvent.Message, "1 in-transit vehicle excluded") {
		t.Fatalf("partial result: event=%+v, import=%v", scrapeEvent, imported)
	}
	secondStock = "6N5626"
	scrapeEvent, imported = run()
	if len(imported) != 2 || imported[1].VIN != secondVIN || imported[1].StockID != secondStock ||
		scrapeEvent.Status != "success" || scrapeEvent.ItemCount != 2 || scrapeEvent.Message != "" || providerCalls != 3 {
		t.Fatalf("stock assignment result: event=%+v, import=%v, provider calls=%d", scrapeEvent, imported, providerCalls)
	}
}
