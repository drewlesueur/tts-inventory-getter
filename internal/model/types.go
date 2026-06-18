package model

import (
	"encoding/json"
	"time"
)

type InventoryItem struct {
	StockID           string   `json:"stockId"`
	Stock             string   `json:"stock"`
	URL               string   `json:"url"`
	Website           string   `json:"website"`
	DealerID          string   `json:"dealerId"`
	Title             string   `json:"title"`
	Style             string   `json:"style"`
	Year              string   `json:"year"`
	Make              string   `json:"make"`
	Model             string   `json:"model"`
	Color             string   `json:"color"`
	VIN               string   `json:"vin"`
	PrimaryImage      string   `json:"primaryImage"`
	Images            []string `json:"images"`
	PhotoURLs         []string `json:"photoURLs"`
	Price             string   `json:"price"`
	VehicleListPrice  string   `json:"vehicleListPrice"`
	Mileage           string   `json:"mileage"`
	Engine            string   `json:"engine"`
	Cylinders         string   `json:"cylinders"`
	Horsepower        string   `json:"horsepower"`
	Torque            string   `json:"torque"`
	Transmission      string   `json:"transmission"`
	TransmissionType  string   `json:"transmissionType"`
	DriveType         string   `json:"driveType"`
	FuelType          string   `json:"fuelType"`
	FuelCapacity      string   `json:"fuelCapacity"`
	FuelEconomy       string   `json:"fuelEconomy"`
	MilesPerGallon    string   `json:"milesPerGallon"`
	MilesPerLiter     string   `json:"milesPerLiter"`
	CityMPG           string   `json:"cityMPG"`
	HighwayMPG        string   `json:"highwayMPG"`
	CityMPL           string   `json:"cityMPL"`
	HighwayMPL        string   `json:"highwayMPL"`
	BodyType          string   `json:"bodyType"`
	SeatInfo          string   `json:"seatInfo"`
	PassengerCapacity string   `json:"passengerCapacity"`
	TireInfo          string   `json:"tireInfo"`
	FrontTire         string   `json:"frontTire"`
	RearTire          string   `json:"rearTire"`
	WheelInfo         string   `json:"wheelInfo"`
	FrontWheel        string   `json:"frontWheel"`
	RearWheel         string   `json:"rearWheel"`
}

func (i InventoryItem) MarshalJSON() ([]byte, error) {
	type inventoryItemAlias InventoryItem
	out := inventoryItemAlias(i)
	if out.Images == nil {
		out.Images = []string{}
	}
	if out.PhotoURLs == nil {
		out.PhotoURLs = []string{}
	}
	return json.Marshal(out)
}

type RunStatus string

const (
	RunStatusRunning RunStatus = "running"
	RunStatusSuccess RunStatus = "success"
	RunStatusPartial RunStatus = "partial_success"
	RunStatusFailed  RunStatus = "failed"
)

type RunSummary struct {
	RunID          string    `json:"runId" bson:"runId"`
	DealershipID   string    `json:"dealershipId" bson:"dealershipId"`
	SourceURL      string    `json:"sourceUrl" bson:"sourceUrl"`
	Status         RunStatus `json:"status" bson:"status"`
	StartedAt      time.Time `json:"startedAt" bson:"startedAt"`
	FinishedAt     time.Time `json:"finishedAt,omitempty" bson:"finishedAt,omitempty"`
	TotalItems     int       `json:"totalItems" bson:"totalItems"`
	SuccessItems   int       `json:"successItems" bson:"successItems"`
	FailedItems    int       `json:"failedItems" bson:"failedItems"`
	FailureReason  string    `json:"failureReason,omitempty" bson:"failureReason,omitempty"`
	ErrorCount     int       `json:"errorCount" bson:"errorCount"`
	IdempotencyKey string    `json:"idempotencyKey,omitempty" bson:"idempotencyKey,omitempty"`
}

type ScrapeResult struct {
	ResultID       string            `json:"resultId"`
	DealershipID   string            `json:"dealershipId"`
	SourceURL      string            `json:"sourceUrl"`
	Status         RunStatus         `json:"status"`
	StartedAt      time.Time         `json:"startedAt"`
	FinishedAt     time.Time         `json:"finishedAt,omitempty"`
	TotalItems     int               `json:"totalItems"`
	SuccessItems   int               `json:"successItems"`
	FailedItems    int               `json:"failedItems"`
	FailureReason  string            `json:"failureReason,omitempty"`
	ErrorCount     int               `json:"errorCount"`
	AttemptCount   int               `json:"attemptCount"`
	LastError      string            `json:"lastError,omitempty"`
	IsRetrying     bool              `json:"isRetrying,omitempty"`
	NextRetryAt    time.Time         `json:"nextRetryAt,omitempty"`
	ProgressStage  string            `json:"progressStage,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey,omitempty"`
	Items          []InventoryItem   `json:"items,omitempty"`
	Errors         []StructuredError `json:"errors,omitempty"`
}

type StructuredError struct {
	Code    string `json:"code" bson:"code"`
	Message string `json:"message" bson:"message"`
	ItemURL string `json:"itemUrl,omitempty" bson:"itemUrl,omitempty"`
}

func ErrorResponse(code, message string) map[string]any {
	return map[string]any{
		"status": "error",
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	}
}
