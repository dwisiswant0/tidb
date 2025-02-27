// Copyright 2017 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ddl

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/pingcap/errors"
	"github.com/pingcap/failpoint"
	"github.com/pingcap/tidb/ddl/syncer"
	"github.com/pingcap/tidb/parser/ast"
	"github.com/pingcap/tidb/parser/charset"
	"github.com/pingcap/tidb/parser/model"
	"github.com/pingcap/tidb/sessionctx"
	clientv3 "go.etcd.io/etcd/client/v3"
)

var _ syncer.SchemaSyncer = &MockSchemaSyncer{}

const mockCheckVersInterval = 2 * time.Millisecond

// MockSchemaSyncer is a mock schema syncer, it is exported for tesing.
type MockSchemaSyncer struct {
	selfSchemaVersion int64
	globalVerCh       chan clientv3.WatchResponse
	mockSession       chan struct{}
}

// NewMockSchemaSyncer creates a new mock SchemaSyncer.
func NewMockSchemaSyncer() syncer.SchemaSyncer {
	return &MockSchemaSyncer{}
}

// Init implements SchemaSyncer.Init interface.
func (s *MockSchemaSyncer) Init(ctx context.Context) error {
	s.globalVerCh = make(chan clientv3.WatchResponse, 1)
	s.mockSession = make(chan struct{}, 1)
	return nil
}

// GlobalVersionCh implements SchemaSyncer.GlobalVersionCh interface.
func (s *MockSchemaSyncer) GlobalVersionCh() clientv3.WatchChan {
	return s.globalVerCh
}

// WatchGlobalSchemaVer implements SchemaSyncer.WatchGlobalSchemaVer interface.
func (s *MockSchemaSyncer) WatchGlobalSchemaVer(context.Context) {}

// UpdateSelfVersion implements SchemaSyncer.UpdateSelfVersion interface.
func (s *MockSchemaSyncer) UpdateSelfVersion(ctx context.Context, version int64) error {
	atomic.StoreInt64(&s.selfSchemaVersion, version)
	return nil
}

// Done implements SchemaSyncer.Done interface.
func (s *MockSchemaSyncer) Done() <-chan struct{} {
	return s.mockSession
}

// CloseSession mockSession, it is exported for testing.
func (s *MockSchemaSyncer) CloseSession() {
	close(s.mockSession)
}

// Restart implements SchemaSyncer.Restart interface.
func (s *MockSchemaSyncer) Restart(_ context.Context) error {
	s.mockSession = make(chan struct{}, 1)
	return nil
}

// OwnerUpdateGlobalVersion implements SchemaSyncer.OwnerUpdateGlobalVersion interface.
func (s *MockSchemaSyncer) OwnerUpdateGlobalVersion(ctx context.Context, version int64) error {
	select {
	case s.globalVerCh <- clientv3.WatchResponse{}:
	default:
	}
	return nil
}

// OwnerCheckAllVersions implements SchemaSyncer.OwnerCheckAllVersions interface.
func (s *MockSchemaSyncer) OwnerCheckAllVersions(ctx context.Context, latestVer int64) error {
	ticker := time.NewTicker(mockCheckVersInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			failpoint.Inject("checkOwnerCheckAllVersionsWaitTime", func(v failpoint.Value) {
				if v.(bool) {
					panic("shouldn't happen")
				}
			})
			return errors.Trace(ctx.Err())
		case <-ticker.C:
			ver := atomic.LoadInt64(&s.selfSchemaVersion)
			if ver >= latestVer {
				return nil
			}
		}
	}
}

// Close implements SchemaSyncer.Close interface.
func (*MockSchemaSyncer) Close() {}

type mockDelRange struct {
}

// newMockDelRangeManager creates a mock delRangeManager only used for test.
func newMockDelRangeManager() delRangeManager {
	return &mockDelRange{}
}

// addDelRangeJob implements delRangeManager interface.
func (*mockDelRange) addDelRangeJob(_ context.Context, _ *model.Job) error {
	return nil
}

// removeFromGCDeleteRange implements delRangeManager interface.
func (*mockDelRange) removeFromGCDeleteRange(_ context.Context, _ int64, _ []int64) error {
	return nil
}

// start implements delRangeManager interface.
func (dr *mockDelRange) start() {}

// clear implements delRangeManager interface.
func (dr *mockDelRange) clear() {}

// MockTableInfo mocks a table info by create table stmt ast and a specified table id.
func MockTableInfo(ctx sessionctx.Context, stmt *ast.CreateTableStmt, tableID int64) (*model.TableInfo, error) {
	chs, coll := charset.GetDefaultCharsetAndCollate()
	cols, newConstraints, err := buildColumnsAndConstraints(ctx, stmt.Cols, stmt.Constraints, chs, coll)
	if err != nil {
		return nil, errors.Trace(err)
	}
	tbl, err := BuildTableInfo(ctx, stmt.Table.Name, cols, newConstraints, "", "")
	if err != nil {
		return nil, errors.Trace(err)
	}
	tbl.ID = tableID

	if err = setTableAutoRandomBits(ctx, tbl, stmt.Cols); err != nil {
		return nil, errors.Trace(err)
	}

	// The specified charset will be handled in handleTableOptions
	if err = handleTableOptions(stmt.Options, tbl); err != nil {
		return nil, errors.Trace(err)
	}

	return tbl, nil
}
