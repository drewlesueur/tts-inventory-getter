package api

import (
    "time"
    "github.com/google/uuid"
    "github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
    "github.com/drewlesueur/tts-inventory-getter/internal/model"
    "go.uber.org/zap"
)

func (s *Server) reportScrapeActivity(result model.ScrapeResult, trigger string) {
    if s.invClient == nil { return }
    message := result.FailureReason
    if message == "" { message = result.LastError }
    if message == "" && len(result.Errors) > 0 { message = result.Errors[0].Message }
    if result.ResultID == "" { result.ResultID = uuid.NewString() }
    if result.StartedAt.IsZero() { result.StartedAt = time.Now().UTC() }
    if result.FinishedAt.IsZero() { result.FinishedAt = time.Now().UTC() }
    if result.Status == "cached" && result.SuccessItems == 0 {
        result.Status = model.RunStatusFailed
        message = "Cached inventory is empty"
    }
    event := inventoryapi.Activity{ID: result.ResultID, Phase: "scrape", Trigger: trigger, Status: string(result.Status), SourceURL: result.SourceURL, StartedAt: result.StartedAt.Format(time.RFC3339Nano), FinishedAt: result.FinishedAt.Format(time.RFC3339Nano), ItemCount: result.SuccessItems, Message: message}
    if err := s.invClient.ReportActivity(result.DealershipID, "", event); err != nil { s.logger.Warn("scrape activity report failed", zap.Error(err)) }
}

func inventoryActivityTrigger(value string) string {
    switch value { case "manual", "scheduled", "signup": return value }
    return "service"
}

func scrapeActivityTrigger(options *ScrapeOptions) string {
    if options == nil || options.ActivityTrigger == "" { return "manual" }
    return inventoryActivityTrigger(options.ActivityTrigger)
}
