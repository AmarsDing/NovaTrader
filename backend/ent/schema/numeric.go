package schema

import "entgo.io/ent/dialect"

// numericPrice 价格列。A 股报价两位小数，保留四位给复权中间结果。
var numericPrice = map[string]string{dialect.Postgres: "numeric(12,4)"}

// numericMoney 金额列，单位元。
var numericMoney = map[string]string{dialect.Postgres: "numeric(18,2)"}
