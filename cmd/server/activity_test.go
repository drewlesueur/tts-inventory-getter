package main

import (
    "context"
    "encoding/json"
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
