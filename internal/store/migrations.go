package store

import (
	"context"
	"database/sql"

	m "github.com/andrey-vk/wdbgp/internal/store/migrations"
)

type Migration struct {
	Version int
	Name    string
	Func    func(context.Context, *sql.Tx) error
	NoTxSQL func(ctx context.Context, db *sql.DB) error // nil for most
}

var migrations = []Migration{
	{1, "initial schema", m.V001, nil},
	{2, "add OpenCCK IPv6 feeds", m.V002, nil},
	{3, "add lookup indexes", m.V003, nil},
	{4, "deduplicate feeds by URL", m.V004, nil},
	{5, "add route filters", m.V005, nil},
	{6, "add user route filter mode", m.V006, nil},
	{7, "drop legacy OpenCCK feed category selections", m.V007, nil},
	{8, "drop orphan route selections", m.V008, nil},
	{9, "add catalog modes", m.V009, nil},
	{10, "switch IPRanges source to antonme", m.V010, nil},
	{11, "add script feed adapters", m.V011, nil},
	{12, "query optimization indexes", m.V012, nil},
	{13, "add catalog communities", m.V013, nil},
	{14, "add web auth", m.V014, nil},
	{15, "add app settings", m.V015, nil},
	{16, "make user credentials login globally unique", m.V016, nil},
	{17, "add sync_interval to feeds", m.V017, nil},
	{18, "add data column to feeds", m.V018, nil},
	{19, "add sing-box SRS catalog mode", m.V019, nil},
	{20, "add adapter upgrade support, catalog mode M:M", m.V020, m.V020NoTxSQL},
	{23, "add active_dial column to users", m.V023, nil},
	{24, "add adapter fork columns", m.V024, nil},
	{25, "add restrict_hosts and allowed_hosts to feeds", m.V025, nil},
	{26, "add metric snapshots", m.V026, nil},
	{27, "add is_builtin to feed_adapters", m.V027, nil},
	{28, "drop allowed_hosts from feed_adapters", m.V028, nil},
	{29, "move route filters to app_settings", m.V029, nil},
}
