package model

// DiagnosticEvent describes an observation, not an inferred root cause. A zero
// HTTPStatus means no status was observed (it must never be read as HTTP 200).
type DiagnosticEvent struct {
    At string `json:"at"`
    Attempt int `json:"attempt,omitempty"`
    Stage string `json:"stage"`
    Method string `json:"method,omitempty"`
    URL string `json:"url,omitempty"`
    FinalURL string `json:"finalUrl,omitempty"`
    HTTPStatus int `json:"httpStatus,omitempty"`
    DurationMS int64 `json:"durationMs,omitempty"`
    Bytes int `json:"bytes,omitempty"`
    Count int `json:"count,omitempty"`
    Title string `json:"title,omitempty"`
    BlockMarker string `json:"blockMarker,omitempty"`
    BodySHA256 string `json:"bodySha256,omitempty"`
    Message string `json:"message,omitempty"`
}

type Diagnostics struct {
    Version string `json:"version"`
    Events []DiagnosticEvent `json:"events"`
    DroppedEvents int `json:"droppedEvents,omitempty"`
}
