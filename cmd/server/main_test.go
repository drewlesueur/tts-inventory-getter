package main

import (
	"testing"

	"github.com/drewlesueur/tts-inventory-getter/internal/inventoryapi"
)

func TestPageMatchesSchedule(t *testing.T) {
	var daily, weekly, legacy, dailyWithBadLabel, weeklyWithBadLabel inventoryapi.PageEntry
	daily.Schedule.Type = "daily"
	weekly.Schedule.Type = "weekly"
	dailyWithBadLabel.Schedule.Type = "weekly"
	dailyWithBadLabel.ScrapeFrequencyMinutes = 1440
	weeklyWithBadLabel.Schedule.Type = "daily"
	weeklyWithBadLabel.ScrapeFrequencyMinutes = 10080

	tests := []struct {
		name     string
		page     inventoryapi.PageEntry
		schedule string
		want     bool
	}{
		{name: "daily page on daily run", page: daily, schedule: "daily", want: true},
		{name: "weekly page skipped on daily run", page: weekly, schedule: "daily", want: false},
		{name: "weekly page on weekly run", page: weekly, schedule: "weekly", want: true},
		{name: "legacy page remains daily", page: legacy, schedule: "daily", want: true},
		{name: "legacy page excluded from weekly", page: legacy, schedule: "weekly", want: false},
		{name: "daily frequency wins over stale weekly label", page: dailyWithBadLabel, schedule: "daily", want: true},
		{name: "weekly frequency wins over stale daily label", page: weeklyWithBadLabel, schedule: "weekly", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := pageMatchesSchedule(tt.page, tt.schedule); got != tt.want {
				t.Fatalf("pageMatchesSchedule() = %v, want %v", got, tt.want)
			}
		})
	}
}
