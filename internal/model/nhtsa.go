package model

import "time"

const NHTSAMaxPDF = 20_000_000

type NHTSAError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type NHTSALookup struct {
	ResultID     string      `json:"resultId"`
	Status       string      `json:"status"`
	VIN          string      `json:"vin"`
	DealershipID string      `json:"dealershipId"`
	LookupDate   *time.Time  `json:"lookupDate"`
	Error        *NHTSAError `json:"error,omitempty"`
}
