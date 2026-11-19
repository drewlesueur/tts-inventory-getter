package scrape

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "net/http"
    "net/http/httptest"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "testing"
    "time"

    "github.com/drewlesueur/tts-inventory-getter/internal/model"
)

func TestDiagnosticsRedaction(t *testing.T) {
    cases:=[]string{
        `Authorization: Bearer fixture-secret`,
        `{"apiKey":"fixture-secret","ok":true}`,
        `{'password': 'fixture-secret'}`,
        `Cookie: datadome=fixture-secret`,
        `https://user:fixture-secret@example.test/inventory?token=fixture-secret#fixture-secret`,
        `socks5://user:fixture-secret@proxy.test:1080/path?password=fixture-secret`,
        `ftp://user:fixture-secret@example.test/file?token=fixture-secret`,
        `Bearer fixture-secret`,
    }
    for _,input:=range cases {
        if got:=DiagnosticText(input);strings.Contains(got,"fixture-secret") {t.Errorf("secret retained: %q",got)}
    }
    if got:=DiagnosticURL("https://user:pass@example.test/cars?page=2#part");got!="https://example.test/cars" {t.Fatal(got)}
    if got:=DiagnosticText("HTTP 403: browser dependency unavailable");got!="HTTP 403: browser dependency unavailable" {t.Fatal(got)}
    wrapped:=diagnosticError(context.DeadlineExceeded, `{"token":"fixture-secret"}`)
    if !errors.Is(wrapped,context.DeadlineExceeded) || strings.Contains(wrapped.Error(),"fixture-secret") {t.Fatal(wrapped)}
}

func TestDiagnosticsBoundsAndSnapshotIsolation(t *testing.T) {
    _,d:=WithDiagnostics(context.Background())
    for i:=0;i<200;i++ {d.Add(model.DiagnosticEvent{Stage:fmt.Sprint(i),URL:"https://example.test/"+strings.Repeat("x",1000),Message:strings.Repeat("m",4000),Title:strings.Repeat("t",1000)})}
    snap:=d.Snapshot()
    if len(snap.Events)!=160 || snap.DroppedEvents!=40 {t.Fatalf("events=%d dropped=%d",len(snap.Events),snap.DroppedEvents)}
    if snap.Events[0].Stage!="0" || snap.Events[15].Stage!="15" || snap.Events[16].Stage!="56" || snap.Events[159].Stage!="199" {t.Fatal("lost opening context or latest evidence")}
    for _,e:=range snap.Events {
        if len(e.URL)>600 || len(e.Title)>200 || len(e.Message)>3020 {t.Fatal("unbounded field")}
    }
    b,err:=json.Marshal(snap);if err!=nil {t.Fatal(err)}
    if len(b)>700000 {t.Fatalf("unexpected per-result size: %d",len(b))}
    snap.Events[0].Stage="mutated"
    if d.Snapshot().Events[0].Stage!="0" {t.Fatal("snapshot aliases live collector")}
}

func TestDiagnosticsLateRetryAttribution(t *testing.T) {
    ctx,d:=WithDiagnostics(context.Background())
    first:=WithDiagnosticAttempt(ctx,1)
    second:=WithDiagnosticAttempt(ctx,2)
    d.SetAttempt(2)
    // Model first-attempt workers finishing after the next attempt has begun.
    var wg sync.WaitGroup
    for i:=0;i<40;i++ {wg.Add(1);go func(){defer wg.Done();RecordDiagnostic(first,model.DiagnosticEvent{Stage:"late-first"});_ = d.Snapshot()}()}
    RecordDiagnostic(second,model.DiagnosticEvent{Stage:"second"})
    wg.Wait()
    for _,e:=range d.Snapshot().Events {
        want:=1;if e.Stage=="second" {want=2}
        if e.Attempt!=want {t.Fatalf("%s attributed to %d instead of %d",e.Stage,e.Attempt,want)}
    }
}

func TestDiagnosticsBoundedStderrAndStructuredEvents(t *testing.T) {
    var b boundedDiagnosticBuffer
    input:="password="+strings.Repeat("secret",4000)+"\nSCRAPE_DIAGNOSTIC {\"stage\":\"http_response\",\"httpStatus\":403,\"url\":\"https://example.test/?token=fixture-secret\"}\n"
    n,err:=b.Write([]byte(input));if err!=nil || n!=len(input) {t.Fatal(n,err)}
    if len(b.data)>16384 || strings.Contains(b.String(),"secretsecret") {t.Fatal("partial secret line retained")}
    ctx,d:=WithDiagnostics(context.Background());recordSubprocessDiagnostics(ctx,"python","",b.String())
    snap:=d.Snapshot();if snap.Events[0].HTTPStatus!=403 || strings.Contains(snap.Events[0].URL,"fixture-secret") {t.Fatalf("bad structured event: %+v",snap.Events[0])}
    var noNewline boundedDiagnosticBuffer;noNewline.Write([]byte(strings.Repeat("x",20000)))
    if strings.Contains(noNewline.String(),"xxxx") {t.Fatal("unidentifiable partial line retained")}
}

func TestDiagnosticsHTTPAndPageMetadata(t *testing.T) {
    server:=httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
        if r.URL.Path=="/start" {http.Redirect(w,r,"/blocked?token=fixture-secret",http.StatusFound);return}
        w.WriteHeader(403);fmt.Fprint(w,"<title>Access denied</title><p>private-page-content</p>")
    }));defer server.Close()
    ctx,d:=WithDiagnostics(context.Background())
    req,_:=http.NewRequestWithContext(ctx,"GET",server.URL+"/start?token=fixture-secret",nil)
    resp,err:=diagnosticHTTPDo(server.Client(),req);if err!=nil {t.Fatal(err)};resp.Body.Close()
    html:="<title>Access denied</title><p>private-page-content</p>"
    recordPageDiagnostic(ctx,"fetch","fixture",server.URL,"",html,403,time.Now(),nil)
    snap:=d.Snapshot();first:=snap.Events[0];last:=snap.Events[1]
    if first.HTTPStatus!=403 || !strings.HasSuffix(first.FinalURL,"/blocked") {t.Fatalf("wrong final response: %+v",first)}
    if last.Title!="Access denied" || last.BlockMarker!="access denied" || len(last.BodySHA256)!=64 || last.Bytes!=len(html) {t.Fatalf("wrong page metadata: %+v",last)}
    data,_:=json.Marshal(snap);if strings.Contains(string(data),"fixture-secret") || strings.Contains(string(data),"private-page-content") {t.Fatal("sensitive content retained")}
}

func TestDiagnosticsBatchErrorsPreservePartialRowsWithoutSecrets(t *testing.T) {
    script:=filepath.Join(t.TempDir(),"fixture.sh")
    // Only a local shell fixture is executed; no Python/browser dependency or network.
    source:=`printf '%s\n' '{"url":"https://fixture.invalid/car","html":"<title>Fixture vehicle</title>"}' '{"error":"fixture failed"}'
printf '%s\n' 'Authorization: Bearer fixture-secret' 'SCRAPE_DIAGNOSTIC {"stage":"http_response","method":"fixture","httpStatus":503}' >&2
exit 1
`
    if err:=os.WriteFile(script,[]byte(source),0600);err!=nil {t.Fatal(err)}
    ctx,d:=WithDiagnostics(context.Background())
    b:=BatchDetailFetcher{PythonBin:"/bin/sh",ScriptPath:script}
    rows,err:=b.fetchAll(ctx,[]string{"https://fixture.invalid/car"})
    if err==nil || !strings.Contains(err.Error(),"fixture failed") || strings.Contains(err.Error(),"fixture-secret") {t.Fatal(err)}
    if len(rows)!=1 {t.Fatalf("lost partial rows: %v",rows)}
    data,_:=json.Marshal(d.Snapshot());if strings.Contains(string(data),"fixture-secret") || !strings.Contains(string(data),`"httpStatus":503`) {t.Fatalf("bad diagnostics: %s",data)}
}
