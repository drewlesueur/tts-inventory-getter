package api

import (
    "context"
    "time"
    "github.com/google/uuid"
    "go.uber.org/zap"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
    "github.com/drewlesueur/tts-inventory-getter/internal/scrape"
)

func diagnosticErrorResponse(code string, err error, diagnostics *scrape.Diagnostics) map[string]any {
    diagnostics.Add(model.DiagnosticEvent{Stage:"request_failed",Method:code,Message:err.Error()})
    result := model.ErrorResponse(code, scrape.DiagnosticText(err.Error()))
    result["diagnostics"] = diagnostics.Snapshot()
    return result
}

// Preflight failures have no asynchronous job yet. Give them a result ID too,
// so configuration/cache failures remain inspectable after the response.
func (s *Server) failedDiagnosticResponse(code string, err error, diagnostics *scrape.Diagnostics, dealer, source string) map[string]any {
    result := diagnosticErrorResponse(code,err,diagnostics)
    id:=uuid.NewString()
    record:=model.ScrapeResult{ResultID:id,DealershipID:dealer,SourceURL:source,Status:model.RunStatusFailed,StartedAt:time.Now().UTC(),FinishedAt:time.Now().UTC(),FailureReason:scrape.DiagnosticText(err.Error()),Diagnostics:diagnostics.Snapshot()}
    ctx,cancel:=context.WithTimeout(context.Background(),3*time.Second)
    defer cancel()
    if saveErr:=s.store.UpsertResult(ctx,record);saveErr!=nil {
        s.logger.Warn("failed to persist preflight diagnostics",zap.String("resultId",id),zap.Error(saveErr))
    } else {result["resultId"]=id}
    return result
}

func runDiagnosticErrorResponse(code string, err error, diagnostics *scrape.Diagnostics, resultID string) map[string]any {
    result:=diagnosticErrorResponse(code,err,diagnostics)
    result["resultId"]=resultID
    return result
}
