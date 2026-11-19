package inventoryapi

import (
    "context"
    "encoding/json"
    "errors"
    "io"
    "net/http"
    "strings"
    "testing"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestInventoryActivityImportOutcomes(t *testing.T) {
    for _,tc:=range []struct{name,body,status string; code int; transportError bool}{
        {name:"saved",body:`{"status":200,"data":{"created":2,"updated":1}}`,status:"success",code:200},
        {name:"partial",body:`{"status":200,"data":{"created":1,"skipped":2}}`,status:"partial_success",code:200},
        {name:"application error",body:`{"status":400}`,status:"failed",code:200},
        {name:"http error",body:`failure`,status:"failed",code:503},
        {name:"invalid acknowledgement",body:`not json`,status:"unknown",code:200},
        {name:"network error",status:"unknown",transportError:true},
    } {
        t.Run(tc.name,func(t *testing.T){
            var report struct { DealershipID string `json:"dealershipId"`; AccountID string `json:"accountID"`; Event Activity `json:"event"` }
            reportCount:=0
            client:=Client{BaseURL:"http://fixture.invalid",ServiceKey:"key"}
            client.HTTPClient=&http.Client{Transport:roundTripperFunc(func(r *http.Request)(*http.Response,error){
                if r.Header.Get("X-Service-Key")!="key" { t.Fatal("missing service authentication") }
                if r.URL.Path=="/api/inventory/activity" {
                    reportCount++;if err:=json.NewDecoder(r.Body).Decode(&report);err!=nil {t.Fatal(err)}
                    return &http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(`{"status":200}`))},nil
                }
                if tc.transportError { return nil,errors.New("fixture connection failed") }
                var request upsertRequest
                if err:=json.NewDecoder(r.Body).Decode(&request);err!=nil || request.ActivityID=="" {t.Fatal("missing activity ID")}
                return &http.Response{StatusCode:tc.code,Body:io.NopCloser(strings.NewReader(tc.body))},nil
            })}
            err:=client.UpsertInventoryWithActivity(context.Background(),"a1","d1",[]model.InventoryItem{{StockID:"one"}},"scheduled","https://fixture.invalid/inventory")
            if (err!=nil)!=(tc.status=="failed" || tc.status=="unknown") {t.Fatalf("error=%v",err)}
            if reportCount!=1 || report.Event.Status!=tc.status || report.Event.Phase!="import" || report.Event.Trigger!="scheduled" || report.DealershipID!="d1" || report.AccountID!="a1" {t.Fatalf("unexpected report: %+v",report)}
            if tc.name=="saved" && report.Event.ItemCount!=3 {t.Fatal("wrong saved count")}
        })
    }
}

func TestInventoryActivityReportingFailureDoesNotFailImport(t *testing.T) {
    client:=Client{BaseURL:"http://fixture.invalid",HTTPClient:&http.Client{Transport:roundTripperFunc(func(r *http.Request)(*http.Response,error){
        if r.URL.Path=="/api/inventory/activity" {return nil,errors.New("report unavailable")}
        return &http.Response{StatusCode:200,Body:io.NopCloser(strings.NewReader(`{"status":200}`))},nil
    })}}
    if err:=client.UpsertInventory(context.Background(),"a1","d1",[]model.InventoryItem{{StockID:"one"}});err!=nil {t.Fatal(err)}
}
