package gorm

import "database/sql"

type sqlCommon interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Prepare(query string) (*sql.Stmt, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
	QueryRow(query string, args ...interface{}) *sql.Row
}

// 事务接口
type sqlDb interface {
	Begin() (*sql.Tx, error)
}

// 事务接口
type sqlTx interface {
	Commit() error
	Rollback() error
}
