package model

// ParhamReportSnapshot stores the cumulative per-client and per-location
// traffic totals captured when a full daily report is built, so the next
// report can diff against them and describe the period since the last one.
// Only the newest few rows are kept.
type ParhamReportSnapshot struct {
	Id      int   `json:"id" gorm:"primaryKey;autoIncrement"`
	TakenAt int64 `json:"takenAt"`
	Data    string `json:"data"`
}
