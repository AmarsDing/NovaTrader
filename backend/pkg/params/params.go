package params

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"server/ent"
	"server/ent/strategyconfig"
	"server/ent/strategyversion"
)

// Put 更新一个参数，并把更新后的全量配置写成新版本。旧的生效版本会被关掉。
func Put(ctx context.Context, client *ent.Client, key, value, valueType, description string) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	snap, err := snapshot(ctx, tx)
	if err != nil {
		return err
	}
	row, err := tx.StrategyConfig.Query().Where(strategyconfig.ConfigKeyEQ(key)).Only(ctx)
	if ent.IsNotFound(err) {
		if err := tx.StrategyConfig.Create().
			SetConfigKey(key).
			SetConfigValue(value).
			SetValueType(valueType).
			SetDescription(description).
			Exec(ctx); err != nil {
			return err
		}
	} else if err != nil {
		return err
	} else if err := tx.StrategyConfig.UpdateOneID(row.ID).
		SetConfigValue(value).
		SetValueType(valueType).
		SetDescription(description).
		Exec(ctx); err != nil {
		return err
	}
	snap[key] = value
	if err := activate(ctx, tx, snap); err != nil {
		return err
	}
	return tx.Commit()
}

// PutMany 在一个事务里写入多个参数并生成一个新版本，返回新版本和写入前的生效版本。
// 写入前没有生效版本时，先把当前全量配置存成一个基线版本，保证之后能回滚。
func PutMany(ctx context.Context, client *ent.Client, values map[string]string, valueType, description string) (newID, prevID int, err error) {
	tx, err := client.Tx(ctx)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	snap, err := snapshot(ctx, tx)
	if err != nil {
		return 0, 0, err
	}
	prev, err := tx.StrategyVersion.Query().Where(strategyversion.IsActive(true)).Only(ctx)
	switch {
	case ent.IsNotFound(err):
		if prev, err = tx.StrategyVersion.Create().
			SetVersionName("v" + strconv.FormatInt(time.Now().UnixNano(), 10) + "-base").
			SetConfigSnapshot(snap).SetIsActive(false).Save(ctx); err != nil {
			return 0, 0, fmt.Errorf("params: base version: %w", err)
		}
	case err != nil:
		return 0, 0, err
	}
	for key, value := range values {
		row, err := tx.StrategyConfig.Query().Where(strategyconfig.ConfigKeyEQ(key)).Only(ctx)
		if ent.IsNotFound(err) {
			err = tx.StrategyConfig.Create().SetConfigKey(key).SetConfigValue(value).
				SetValueType(valueType).SetDescription(description).Exec(ctx)
		} else if err == nil {
			err = tx.StrategyConfig.UpdateOneID(row.ID).SetConfigValue(value).SetValueType(valueType).Exec(ctx)
		}
		if err != nil {
			return 0, 0, err
		}
		snap[key] = value
	}
	if err := activate(ctx, tx, snap); err != nil {
		return 0, 0, err
	}
	cur, err := tx.StrategyVersion.Query().Where(strategyversion.IsActive(true)).Only(ctx)
	if err != nil {
		return 0, 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, err
	}
	return cur.ID, prev.ID, nil
}

// Rollback 把参数恢复成某个版本的快照，并把它标成生效版本。
func Rollback(ctx context.Context, client *ent.Client, versionID int) error {
	tx, err := client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	ver, err := tx.StrategyVersion.Get(ctx, versionID)
	if err != nil {
		return err
	}
	current, err := tx.StrategyConfig.Query().All(ctx)
	if err != nil {
		return err
	}
	keep := map[string]struct{}{}
	for key, raw := range ver.ConfigSnapshot {
		value, _ := raw.(string)
		keep[key] = struct{}{}
		row, err := tx.StrategyConfig.Query().Where(strategyconfig.ConfigKeyEQ(key)).Only(ctx)
		if ent.IsNotFound(err) {
			if err := tx.StrategyConfig.Create().SetConfigKey(key).SetConfigValue(value).Exec(ctx); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if err := tx.StrategyConfig.UpdateOneID(row.ID).SetConfigValue(value).Exec(ctx); err != nil {
			return err
		}
	}
	for _, row := range current {
		if _, ok := keep[row.ConfigKey]; ok {
			continue
		}
		if err := tx.StrategyConfig.DeleteOneID(row.ID).Exec(ctx); err != nil {
			return err
		}
	}
	if err := tx.StrategyVersion.Update().SetIsActive(false).Exec(ctx); err != nil {
		return err
	}
	if err := tx.StrategyVersion.UpdateOneID(ver.ID).SetIsActive(true).Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func snapshot(ctx context.Context, tx *ent.Tx) (map[string]any, error) {
	rows, err := tx.StrategyConfig.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(rows))
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

func activate(ctx context.Context, tx *ent.Tx, snap map[string]any) error {
	if err := tx.StrategyVersion.Update().SetIsActive(false).Exec(ctx); err != nil {
		return err
	}
	name := "v" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := tx.StrategyVersion.Create().
		SetVersionName(name).
		SetConfigSnapshot(snap).
		SetIsActive(true).
		Exec(ctx); err != nil {
		return fmt.Errorf("params: version: %w", err)
	}
	return nil
}

// Get 读取当前参数。
func Get(ctx context.Context, client *ent.Client, key string) (string, error) {
	row, err := client.StrategyConfig.Query().Where(strategyconfig.ConfigKeyEQ(key)).Only(ctx)
	if err != nil {
		return "", err
	}
	return row.ConfigValue, nil
}

// ActiveID 返回当前生效版本。
func ActiveID(ctx context.Context, client *ent.Client) (int, error) {
	row, err := client.StrategyVersion.Query().Where(strategyversion.IsActive(true)).Only(ctx)
	if err != nil {
		return 0, err
	}
	return row.ID, nil
}
