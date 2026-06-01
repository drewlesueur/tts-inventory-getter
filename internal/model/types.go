package model

import "time"

type InventoryItem struct {
	StockID           string   `json:"stockId"`
	Stock             string   `json:"stock,omitempty"`
	URL               string   `json:"url"`
	Website           string   `json:"website,omitempty"`
	DealerID          string   `json:"dealerId,omitempty"`
	Title             string   `json:"title"`
	Style             string   `json:"style,omitempty"`
	Year              string   `json:"year,omitempty"`
	Make              string   `json:"make,omitempty"`
	Model             string   `json:"model,omitempty"`
	Color             string   `json:"color,omitempty"`
	VIN               string   `json:"vin,omitempty"`
	PrimaryImage      string   `json:"primaryImage,omitempty"`
	Images            []string `json:"images,omitempty"`
	PhotoURLs         []string `json:"photoURLs,omitempty"`
	Price             string   `json:"price,omitempty"`
	VehicleListPrice  string   `json:"vehicleListPrice,omitempty"`
	Mileage           string   `json:"mileage,omitempty"`
	Engine            string   `json:"engine,omitempty"`
	Cylinders         string   `json:"cylinders,omitempty"`
	Horsepower        string   `json:"horsepower,omitempty"`
	Torque            string   `json:"torque,omitempty"`
	Transmission      string   `json:"transmission,omitempty"`
	TransmissionType  string   `json:"transmissionType,omitempty"`
	DriveType         string   `json:"driveType,omitempty"`
	FuelType          string   `json:"fuelType,omitempty"`
	FuelCapacity      string   `json:"fuelCapacity,omitempty"`
	FuelEconomy       string   `json:"fuelEconomy,omitempty"`
	MilesPerGallon    string   `json:"milesPerGallon,omitempty"`
	MilesPerLiter     string   `json:"milesPerLiter,omitempty"`
	CityMPG           string   `json:"cityMPG,omitempty"`
	HighwayMPG        string   `json:"highwayMPG,omitempty"`
	CityMPL           string   `json:"cityMPL,omitempty"`
	HighwayMPL        string   `json:"highwayMPL,omitempty"`
	BodyType          string   `json:"bodyType,omitempty"`
	SeatInfo          string   `json:"seatInfo,omitempty"`
	PassengerCapacity string   `json:"passengerCapacity,omitempty"`
	TireInfo          string   `json:"tireInfo,omitempty"`
	FrontTire         string   `json:"frontTire,omitempty"`
	RearTire          string   `json:"rearTire,omitempty"`
	WheelInfo         string   `json:"wheelInfo,omitempty"`
	FrontWheel        string   `json:"frontWheel,omitempty"`
	RearWheel         string   `json:"rearWheel,omitempty"`
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
