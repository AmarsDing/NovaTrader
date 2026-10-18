package dataset

import "time"

// Bar contains unadjusted prices only. Factors are independent vintages, never silently overwritten here.
type Bar struct {
	Provenance *Provenance `json:"provenance,omitempty"`
	Symbol     string      `json:"symbol"`
	Time       time.Time   `json:"time"`
	Freq       string      `json:"freq"`
	Open       float64     `json:"open"`
	High       float64     `json:"high"`
	Low        float64     `json:"low"`
	Close      float64     `json:"close"`
	PreClose   *float64    `json:"pre_close"`
	Volume     int64       `json:"volume"`
	Amount     float64     `json:"amount"`
}

type Security struct {
	Symbol     string     `json:"symbol"`
	Name       string     `json:"name"`
	ListDate   *time.Time `json:"list_date"`
	DelistDate *time.Time `json:"delist_date"`
	ST         bool       `json:"st"`
	Suspended  bool       `json:"suspended"`
}

type AdjFactor struct {
	Symbol string    `json:"symbol"`
	Date   time.Time `json:"date"`
	Factor float64   `json:"factor"`
}
type Name struct {
	Symbol string     `json:"symbol"`
	Name   string     `json:"name"`
	Start  time.Time  `json:"start"`
	End    *time.Time `json:"end"`
}
