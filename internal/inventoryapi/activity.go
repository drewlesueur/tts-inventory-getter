package inventoryapi

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "strings"
    "time"
)

// Activity contains no vehicle payloads. ID is stable when a report is retried.
type Activity struct {
    ID string `json:"id"`
    Phase string `json:"phase"`
    Trigger string `json:"trigger"`
    Status string `json:"status"`
    SourceURL string `json:"sourceUrl"`
    StartedAt string `json:"startedAt"`
    FinishedAt string `json:"finishedAt"`
    ItemCount int `json:"itemCount"`
    Message string `json:"message"`
}

func (c *Client) ReportActivity(dealershipID, accountID string, event Activity) error {
    if c == nil || strings.TrimSpace(c.BaseURL) == "" { return nil }
    if len(event.Message) > 2000 { event.Message = event.Message[:2000] }
    if len(event.SourceURL) > 4000 { event.SourceURL = event.SourceURL[:4000] }
    if event.FinishedAt == "" { event.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano) }
    body, err := json.Marshal(struct {
        DealershipID string `json:"dealershipId"`
        AccountID string `json:"accountID,omitempty"`
        Event Activity `json:"event"`
    }{dealershipID, accountID, event})
    if err != nil { return err }
    // Reporting is synchronous within existing work, bounded independently of
    // cancelled scrape contexts, and never changes the scrape/import outcome.
    ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
    defer cancel()
    req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+"/api/inventory/activity", bytes.NewReader(body))
    if err != nil { return err }
    req.Header.Set("Content-Type", "application/json")
    req.Header.Set("X-Service-Key", c.ServiceKey)
    resp, err := c.httpClient().Do(req)
    if err != nil { return err }
    defer resp.Body.Close()
    io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
    if resp.StatusCode != http.StatusOK { return fmt.Errorf("activity report status=%d", resp.StatusCode) }
    return nil
}
