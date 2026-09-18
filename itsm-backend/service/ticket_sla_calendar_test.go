package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func calendarTestInstant(t *testing.T, value string) time.Time {
	t.Helper()
	instant, err := time.Parse(time.RFC3339, value)
	require.NoError(t, err)
	return instant
}

func TestSLACalendarConfiguredZoneUsesSameInstant(t *testing.T) {
	owner := &TicketSLAService{}
	start := calendarTestInstant(t, "2026-09-14T01:00:00Z")
	want := calendarTestInstant(t, "2026-09-14T03:00:00Z")
	for _, location := range []*time.Location{time.UTC, time.FixedZone("different-input-zone", -4*60*60)} {
		t.Run(location.String(), func(t *testing.T) {
			got, err := owner.calculateDeadlineWithBusinessHours(start.In(location), 120, map[string]interface{}{"time_zone": "Asia/Shanghai"})
			require.NoError(t, err)
			require.True(t, want.Equal(got), "got %s; want instant %s", got, want)
		})
	}
}

func TestSLACalendarDateBoundariesAndOverrides(t *testing.T) {
	tests := []struct {
		name, start, want, zone string
		minutes                 int
		holidays, makeup        []interface{}
	}{
		{"ShanghaiMondayBeforeWork", "2026-09-13T23:30:00Z", "2026-09-14T02:00:00Z", "Asia/Shanghai", 60, nil, nil},
		{"HolidayUsesShanghaiDate", "2025-12-31T17:30:00Z", "2026-01-02T02:00:00Z", "Asia/Shanghai", 60, []interface{}{"2026-01-01"}, nil},
		{"WeekendMakeup", "2026-09-18T09:30:00Z", "2026-09-19T02:30:00Z", "Asia/Shanghai", 120, nil, []interface{}{"2026-09-19"}},
		{"OrdinaryWeekendStillSkipped", "2026-09-18T09:30:00Z", "2026-09-21T02:30:00Z", "Asia/Shanghai", 120, nil, nil},
		{"CrossYearWithHolidayAndMakeup", "2026-12-31T09:30:00Z", "2027-01-02T02:30:00Z", "Asia/Shanghai", 120, []interface{}{"2027-01-01"}, []interface{}{"2027-01-02"}},
		{"DSTWeekendPreservesLocalWorkStart", "2026-03-06T22:30:00Z", "2026-03-09T15:30:00Z", "America/New_York", 180, nil, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := map[string]interface{}{"time_zone": tc.zone, "start_time": "09:00", "end_time": "18:00"}
			if tc.holidays != nil {
				cfg["holiday_list"] = tc.holidays
			}
			if tc.makeup != nil {
				cfg["makeup_days"] = tc.makeup
			}
			got, err := (&TicketSLAService{}).calculateDeadlineWithBusinessHours(calendarTestInstant(t, tc.start), tc.minutes, cfg)
			require.NoError(t, err)
			require.True(t, calendarTestInstant(t, tc.want).Equal(got), "got %s; want %s", got, tc.want)
		})
	}
}

func TestSLACalendarInvalidDeclarationsFailClosed(t *testing.T) {
	for name, config := range map[string]map[string]interface{}{
		"unknown zone":          {"time_zone": "Not/A_Zone"},
		"empty zone":            {"time_zone": ""},
		"host dependent zone":   {"time_zone": "Local"},
		"non-string zone":       {"time_zone": 8},
		"invalid makeup date":   {"makeup_days": []interface{}{"2026-02-30"}},
		"invalid makeup list":   {"makeup_days": "2026-09-19"},
		"conflicting override":  {"makeup_days": []interface{}{"2026-09-19"}, "holiday_list": []interface{}{"2026-09-19"}},
		"missing upper bound":   {"valid_from": "2026-01-01"},
		"missing lower bound":   {"valid_until": "2026-12-31"},
		"invalid lower date":    {"valid_from": "2026-02-30", "valid_until": "2026-12-31"},
		"reversed bounds":       {"valid_from": "2026-12-31", "valid_until": "2026-01-01"},
		"makeup outside range":  {"valid_from": "2026-01-01", "valid_until": "2026-12-31", "makeup_days": []interface{}{"2027-01-02"}},
		"holiday outside range": {"valid_from": "2026-01-01", "valid_until": "2026-12-31", "holiday_list": []interface{}{"2025-01-01"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&TicketSLAService{}).calculateDeadlineWithBusinessHours(calendarTestInstant(t, "2026-09-14T01:00:00Z"), 60, config)
			require.Error(t, err)
		})
	}
}

func TestSLACalendarDeclaredCoverageBlocksUnknownDates(t *testing.T) {
	config := map[string]interface{}{"time_zone": "Asia/Shanghai", "valid_from": "2026-01-01", "valid_until": "2026-12-31"}
	for name, tc := range map[string]struct {
		start   string
		minutes int
	}{
		"cross year":          {"2026-12-31T09:30:00Z", 60},
		"before range":        {"2025-12-31T01:00:00Z", 60},
		"after range":         {"2027-01-01T01:00:00Z", 60},
		"zero before range":   {"2025-12-31T01:00:00Z", 0},
		"zero after range":    {"2027-01-01T01:00:00Z", 0},
		"after work last day": {"2026-12-31T10:00:00Z", 1},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := (&TicketSLAService{}).calculateDeadlineWithBusinessHours(calendarTestInstant(t, tc.start), tc.minutes, config)
			require.ErrorContains(t, err, "coverage")
		})
	}
	got, err := (&TicketSLAService{}).calculateDeadlineWithBusinessHours(calendarTestInstant(t, "2026-12-31T09:30:00Z"), 30, config)
	require.NoError(t, err, "a deadline exactly at closing on the last covered date is valid")
	require.True(t, calendarTestInstant(t, "2026-12-31T10:00:00Z").Equal(got))
}

func TestSLACalendarMissingZonePreservesInputLocation(t *testing.T) {
	start := calendarTestInstant(t, "2026-09-14T09:00:00-04:00")
	got, err := (&TicketSLAService{}).calculateDeadlineWithBusinessHours(start, 60, map[string]interface{}{"start_time": "09:00"})
	require.NoError(t, err)
	require.True(t, start.Add(time.Hour).Equal(got))
	got, err = (&TicketSLAService{}).calculateDeadlineWithBusinessHours(start, 60, nil)
	require.NoError(t, err)
	require.True(t, start.Add(time.Hour).Equal(got), "empty calendar remains 24x7")
}
