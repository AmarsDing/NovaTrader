// Package params owns the shared configuration transaction and immutable versions.
package params

import (
	"context"
	"errors"
	"fmt"
	"server/ent"
	"server/ent/confighead"
	"server/ent/strategyconfig"
	"server/ent/strategyversion"
	"strconv"
	"time"
)

var ErrConflict = errors.New("configuration revision conflict")

type Snapshot struct {
	Revision  int               `json:"revision"`
	Values    map[string]string `json:"values"`
	CreatedAt time.Time         `json:"created_at"`
}
type baseKey struct{}

// WithBase makes legacy internal helpers obey an external caller's revision.
func WithBase(ctx context.Context, revision int) context.Context {
	return context.WithValue(ctx, baseKey{}, revision)
}

// ensureHead migrates existing installations without rewriting their versions.
// An idempotent singleton insert establishes the row used by every writer's CAS.
func ensureHead(ctx context.Context, c *ent.Client) error {
	if _, err := c.ConfigHead.Get(ctx, 1); err == nil {
		return nil
	} else if !ent.IsNotFound(err) {
		return err
	}
	old, err := c.StrategyVersion.Query().Where(strategyversion.IsActive(true)).Order(ent.Desc(strategyversion.FieldID)).First(ctx)
	revision := 0
	if err == nil {
		revision = old.ID
	} else if !ent.IsNotFound(err) {
		return err
	}
	return c.ConfigHead.Create().SetID(1).SetRevision(revision).OnConflictColumns(confighead.FieldID).Ignore().Exec(ctx)
}

// Read returns one immutable version, so values cannot be labelled with a later
// writer's revision. Revision zero denotes an installation with no version yet.
func Read(ctx context.Context, c *ent.Client) (Snapshot, error) {
	if err := ensureHead(ctx, c); err != nil {
		return Snapshot{}, err
	}
	head, err := c.ConfigHead.Get(ctx, 1)
	if err != nil {
		return Snapshot{}, err
	}
	if head.Revision > 0 {
		v, err := c.StrategyVersion.Get(ctx, head.Revision)
		if err != nil {
			return Snapshot{}, err
		}
		return fromVersion(v), nil
	}
	rows, err := c.StrategyConfig.Query().All(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	values := map[string]string{}
	for _, r := range rows {
		values[r.ConfigKey] = r.ConfigValue
	}
	// A writer may have installed the first version during this legacy read.
	after, err := c.ConfigHead.Get(ctx, 1)
	if err != nil {
		return Snapshot{}, err
	}
	if after.Revision != 0 {
		return Version(ctx, c, after.Revision)
	}
	return Snapshot{Values: values}, nil
}
func fromVersion(v *ent.StrategyVersion) Snapshot {
	values := map[string]string{}
	for k, raw := range v.ConfigSnapshot {
		if s, ok := raw.(string); ok {
			values[k] = s
		}
	}
	return Snapshot{Revision: v.ID, Values: values, CreatedAt: v.CreatedAt}
}
func CurrentRevision(ctx context.Context, c *ent.Client) (int, error) {
	if err := ensureHead(ctx, c); err != nil {
		return 0, err
	}
	h, err := c.ConfigHead.Get(ctx, 1)
	if err != nil {
		return 0, err
	}
	return h.Revision, nil
}
func Version(ctx context.Context, c *ent.Client, id int) (Snapshot, error) {
	v, err := c.StrategyVersion.Get(ctx, id)
	if err != nil {
		return Snapshot{}, err
	}
	return fromVersion(v), nil
}

// SaveCAS atomically claims the caller's revision before reading or writing any
// values. Competing processes cannot both commit with the same base revision.
func SaveCAS(ctx context.Context, c *ent.Client, base int, values map[string]string, description string) (Snapshot, error) {
	return save(ctx, c, base, values, false, description)
}
func save(ctx context.Context, c *ent.Client, base int, values map[string]string, replace bool, description string) (Snapshot, error) {
	if base < 0 {
		return Snapshot{}, ErrConflict
	}
	if err := ensureHead(ctx, c); err != nil {
		return Snapshot{}, err
	}
	tx, err := c.Tx(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	defer tx.Rollback()
	n, err := tx.ConfigHead.Update().Where(confighead.IDEQ(1), confighead.RevisionEQ(base)).SetUpdatedAt(time.Now()).Save(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if n != 1 {
		return Snapshot{}, ErrConflict
	}
	rows, err := tx.StrategyConfig.Query().All(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	snapshot := map[string]any{}
	if !replace {
		for _, r := range rows {
			snapshot[r.ConfigKey] = r.ConfigValue
		}
	}
	for k, v := range values {
		if v == "" {
			delete(snapshot, k)
		} else {
			snapshot[k] = v
		}
	}
	for k, v := range values {
		if v == "" {
			if _, err = tx.StrategyConfig.Delete().Where(strategyconfig.ConfigKeyEQ(k)).Exec(ctx); err != nil {
				return Snapshot{}, err
			}
			continue
		}
		err = tx.StrategyConfig.Create().SetConfigKey(k).SetConfigValue(v).SetDescription(description).OnConflictColumns(strategyconfig.FieldConfigKey).UpdateNewValues().Exec(ctx)
		if err != nil {
			return Snapshot{}, err
		}
	}
	if replace {
		for _, r := range rows {
			if _, ok := values[r.ConfigKey]; !ok {
				if err = tx.StrategyConfig.DeleteOneID(r.ID).Exec(ctx); err != nil {
					return Snapshot{}, err
				}
			}
		}
	}
	if err = tx.StrategyVersion.Update().SetIsActive(false).Exec(ctx); err != nil {
		return Snapshot{}, err
	}
	ver, err := tx.StrategyVersion.Create().SetVersionName("v" + strconv.FormatInt(time.Now().UnixNano(), 10)).SetConfigSnapshot(snapshot).SetIsActive(true).Save(ctx)
	if err != nil {
		return Snapshot{}, err
	}
	if err = tx.ConfigHead.UpdateOneID(1).SetRevision(ver.ID).Exec(ctx); err != nil {
		return Snapshot{}, err
	}
	if err = tx.Commit(); err != nil {
		return Snapshot{}, err
	}
	return fromVersion(ver), nil
}

// RollbackCAS restores a historical snapshot as a NEW immutable revision.
func RollbackCAS(ctx context.Context, c *ent.Client, base, target int) (Snapshot, error) {
	v, err := Version(ctx, c, target)
	if err != nil {
		return Snapshot{}, err
	}
	return save(ctx, c, base, v.Values, true, fmt.Sprintf("rollback from revision %d", target))
}

// Legacy helpers retain their signatures for internal research workflows. HTTP
// handlers must require a base revision and attach WithBase; no blind retry.
func Put(ctx context.Context, c *ent.Client, key, value, valueType, description string) error {
	_, _, err := PutMany(ctx, c, map[string]string{key: value}, valueType, description)
	return err
}
func PutMany(ctx context.Context, c *ent.Client, values map[string]string, valueType, description string) (newID, prevID int, err error) {
	base, explicit := ctx.Value(baseKey{}).(int)
	if !explicit {
		base, err = CurrentRevision(ctx, c)
		if err != nil {
			return 0, 0, err
		}
	}
	if base == 0 && !explicit { // Preserve a rollback baseline before an internal proposal's first save.
		old, e := Read(ctx, c)
		if e != nil {
			return 0, 0, e
		}
		initial, e := SaveCAS(ctx, c, 0, old.Values, "initial baseline")
		if e != nil {
			return 0, 0, e
		}
		base = initial.Revision
	}
	next, err := SaveCAS(ctx, c, base, values, description)
	return next.Revision, base, err
}
func Rollback(ctx context.Context, c *ent.Client, versionID int) error {
	base, err := CurrentRevision(ctx, c)
	if err != nil {
		return err
	}
	_, err = RollbackCAS(ctx, c, base, versionID)
	return err
}
func Get(ctx context.Context, c *ent.Client, key string) (string, error) {
	s, err := Read(ctx, c)
	if err != nil {
		return "", err
	}
	v, ok := s.Values[key]
	if !ok {
		return "", fmt.Errorf("parameter not found")
	}
	return v, nil
}
func ActiveID(ctx context.Context, c *ent.Client) (int, error) { return CurrentRevision(ctx, c) }
