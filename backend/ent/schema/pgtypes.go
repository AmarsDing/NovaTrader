package schema

import "entgo.io/ent/dialect"

var (
	pgDate        = map[string]string{dialect.Postgres: "date"}
	pgTimestamptz = map[string]string{dialect.Postgres: "timestamptz"}
	// numericAmount 全市场或板块成交额合计，单位元，会超过 numeric(18,2)。
	numericAmount = map[string]string{dialect.Postgres: "numeric(20,2)"}
	// numericRatio 比例或收益率。
	numericRatio = map[string]string{dialect.Postgres: "numeric(12,6)"}
)
