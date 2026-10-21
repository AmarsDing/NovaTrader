package biz

import "context"

// HasFillExec 按 (book, exec_id) 查重（内存账本扫描，与 PG 唯一约束同语义）。
func (m *MemStore) HasFillExec(_ context.Context, book, execID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, f := range m.fills {
		if f.Book == book && f.ExecID == execID {
			return true, nil
		}
	}
	return false, nil
}

// RecordDisposition 内存版留痕：同 (event_id, action) 去重，created=false 表示重复。
func (m *MemStore) RecordDisposition(_ context.Context, d Disposition) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, old := range m.dispositions {
		if old.EventID == d.EventID && old.Action == d.Action {
			return false, nil
		}
	}
	m.dispositions = append(m.dispositions, d)
	return true, nil
}

// Dispositions 暴露已留痕的处置（只给测试断言用）。
func (m *MemStore) Dispositions() []Disposition {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Disposition, len(m.dispositions))
	copy(out, m.dispositions)
	return out
}
