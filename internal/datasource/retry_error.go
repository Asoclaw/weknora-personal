package datasource

import "errors"

var ErrSyncBusy = errors.New("当前同步结束后可重试")
