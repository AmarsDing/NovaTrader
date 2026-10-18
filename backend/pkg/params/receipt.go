package params

import (
	"context"
	"github.com/google/uuid"
	"server/ent"
	"server/ent/configreceipt"
	"time"
)

// InstanceID identifies this process lifetime; a restart cannot inherit an old
// process's acknowledgement. Receipts carry no configuration values.
var InstanceID = uuid.NewString()

type Receipt struct {
	Service    string    `json:"service"`
	Instance   string    `json:"instance"`
	Scope      string    `json:"scope"`
	Revision   int       `json:"revision"`
	Status     string    `json:"status"`
	Detail     string    `json:"detail"`
	ObservedAt time.Time `json:"observed_at"`
}

// Record is called only at a real load/apply point. Allowed statuses and details
// are fixed here, so errors from providers can never echo keys or credentials.
func Record(ctx context.Context, c *ent.Client, service, scope string, revision int, status string) error {
	details := map[string]string{"applied": "参数已解析并安装到运行态", "cache_loaded": "已读取到服务配置缓存；外部连接与调用结果另验", "failed": "配置读取或解析失败；保留上次运行态"}
	detail, ok := details[status]
	if !ok {
		return nil
	}
	now := time.Now()
	n, err := c.ConfigReceipt.Update().Where(configreceipt.ServiceEQ(service), configreceipt.InstanceEQ(InstanceID), configreceipt.ScopeEQ(scope), configreceipt.RevisionLTE(revision)).SetRevision(revision).SetStatus(status).SetDetail(detail).SetObservedAt(now).Save(ctx)
	if err != nil || n > 0 {
		return err
	}
	return c.ConfigReceipt.Create().SetService(service).SetInstance(InstanceID).SetScope(scope).SetRevision(revision).SetStatus(status).SetDetail(detail).SetObservedAt(now).OnConflictColumns(configreceipt.FieldService, configreceipt.FieldInstance, configreceipt.FieldScope).Ignore().Exec(ctx)
}
func Receipts(ctx context.Context, c *ent.Client) ([]Receipt, error) {
	rows, err := c.ConfigReceipt.Query().Order(ent.Desc(configreceipt.FieldObservedAt)).Limit(100).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Receipt, 0, len(rows))
	for _, r := range rows {
		out = append(out, Receipt{r.Service, r.Instance, r.Scope, r.Revision, r.Status, r.Detail, r.ObservedAt})
	}
	return out, nil
}
