package store

import (
    "context"
    "database/sql"
    "path/filepath"
    "reflect"
    "testing"
    "time"

    "github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestDiagnosticsSQLiteLegacyMigrationAndRoundTrip(t *testing.T) {
    // An actual pre-diagnostics schema, including legacy rows/cache data.
    path:=filepath.Join(t.TempDir(),"legacy.sqlite")
    db,err:=sql.Open("sqlite",path);if err!=nil {t.Fatal(err)}
    _,err=db.Exec(`CREATE TABLE scrape_results (
      result_id TEXT PRIMARY KEY, dealership_id TEXT NOT NULL, source_url TEXT NOT NULL,
      status TEXT NOT NULL, started_at TEXT NOT NULL, finished_at TEXT,
      total_items INTEGER NOT NULL DEFAULT 0, success_items INTEGER NOT NULL DEFAULT 0,
      failed_items INTEGER NOT NULL DEFAULT 0, failure_reason TEXT, error_count INTEGER NOT NULL DEFAULT 0,
      attempt_count INTEGER NOT NULL DEFAULT 0,last_error TEXT,is_retrying INTEGER NOT NULL DEFAULT 0,
      next_retry_at TEXT,progress_stage TEXT,idempotency_key TEXT,
      items_json TEXT NOT NULL DEFAULT '[]',errors_json TEXT NOT NULL DEFAULT '[]');
      INSERT INTO scrape_results VALUES ('legacy','dealer','https://fixture.invalid','success','2026-01-01T00:00:00Z','',1,1,0,'',0,1,'',0,'','','legacy-key','[{"stockId":"OLD"}]','[]');`)
    if err!=nil {t.Fatal(err)};db.Close()
    s,err:=NewSQLiteResultStore(path);if err!=nil {t.Fatal(err)};defer s.Close()
    ctx:=context.Background()
    if err:=s.init();err!=nil {t.Fatalf("repeat migration: %v",err)}
    legacy,err:=s.GetResult(ctx,"legacy");if err!=nil {t.Fatal(err)}
    if legacy.Diagnostics!=nil || len(legacy.Items)!=1 || legacy.Items[0].StockID!="OLD" {t.Fatalf("legacy changed: %+v",legacy)}
    byKey,err:=s.FindByIdempotency(ctx,"legacy-key");if err!=nil || byKey.Diagnostics!=nil {t.Fatal(byKey,err)}
    expected:=&model.Diagnostics{Version:"fixture",DroppedEvents:7,Events:[]model.DiagnosticEvent{{At:"2026-01-01T00:00:00Z",Stage:"fetch",Attempt:2,HTTPStatus:403,Message:"blocked"}}}
    legacy.Diagnostics=expected
    if err:=s.UpsertResult(ctx,legacy);err!=nil {t.Fatal(err)}
    for _,idempotent:=range []bool{false,true} {
        var got model.ScrapeResult
        if idempotent {got,err=s.FindByIdempotency(ctx,"legacy-key")} else {got,err=s.GetResult(ctx,"legacy")}
        if err!=nil || !reflect.DeepEqual(got.Diagnostics,expected) || got.Items[0].StockID!="OLD" {t.Fatalf("round trip: %+v %v",got,err)}
    }
    if _,err:=s.db.Exec(`UPDATE scrape_results SET diagnostics_json='{"events":[{"stage":"partial"}],"droppedEvents":"bad"}' WHERE result_id='legacy'`);err!=nil {t.Fatal(err)}
    got,err:=s.GetResult(ctx,"legacy");if err!=nil || got.Diagnostics!=nil || got.Items[0].StockID!="OLD" {t.Fatalf("corrupt optional metadata hid inventory: %+v %v",got,err)}
}

func TestDiagnosticsSQLiteFreshAndLegacyWriter(t *testing.T) {
    s,err:=NewSQLiteResultStore(filepath.Join(t.TempDir(),"new.sqlite"));if err!=nil {t.Fatal(err)};defer s.Close()
    ctx:=context.Background()
    record:=model.ScrapeResult{ResultID:"fresh",DealershipID:"dealer",SourceURL:"https://fixture.invalid",Status:model.RunStatusFailed,StartedAt:time.Now(),Diagnostics:&model.Diagnostics{Version:"fixture",Events:[]model.DiagnosticEvent{{Stage:"failed"}}}}
    if err:=s.UpsertResult(ctx,record);err!=nil {t.Fatal(err)}
    // A legacy writer names its old columns and leaves the additive column alone.
    if _,err:=s.db.Exec(`INSERT INTO scrape_results(result_id,dealership_id,source_url,status,started_at) VALUES('old-writer','dealer','https://fixture.invalid','failed','2026-01-01T00:00:00Z')`);err!=nil {t.Fatal(err)}
    var value string
    if err:=s.db.QueryRow(`SELECT diagnostics_json FROM scrape_results WHERE result_id='old-writer'`).Scan(&value);err!=nil || value!="null" {t.Fatal(value,err)}
    got,err:=s.GetResult(ctx,"fresh");if err!=nil || !reflect.DeepEqual(got.Diagnostics,record.Diagnostics) {t.Fatal(got,err)}
}
