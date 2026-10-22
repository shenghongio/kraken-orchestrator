package postgres

import (
	"errors"
	"fmt"
	"strings"
	
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/shenghongio/kraken-orchestrator/internal/domain"
)

// maxQueryLogLen 限制写入错误的SQL长度，避免超长 SQL
const maxQueryLogLen = 512

// SQLError 封装数据库操作错误，附带操作名，SQL,语句，参数和 pgx 错误详情
// 通过Unwrap 保留原始错误，errors.Is/As 仍然可用
type SQLError struct {
	Operation string // 业务操作名称，例如 "tasl.create"
	Query     string // 执行的SQL语句此处为截断
	Args      string // 参数摘要
	SQLState  string // pgx错误的SQLSTATE
	SQLCode   string // pgx错误的Code
	Detail    string // pgx错误的Detail
	Routine   string // pgx 错误的routine
	Cause     error  // 原始的错误
}

func (e *SQLError) Error() string {
	parts := []string{e.Operation}
	if e.SQLState != "" {
		parts = append(parts, fmt.Sprintf("sqlstate=%s", e.SQLState))
	}
	if e.Query != "" {
		parts = append(parts, fmt.Sprintf("query=%s", e.Query))
	}
	if e.Args != "" {
		parts = append(parts, fmt.Sprintf("args=%s", e.Args))
	}
	if e.Detail != "" {
		parts = append(parts, fmt.Sprintf("detail=%s", e.Detail))
	}
	parts = append(parts, fmt.Sprintf("err=%s", e.Cause))
	return strings.Join(parts, " | ")
}
func (e *SQLError) Unwrap() error {
	return e.Cause
}

// Is 也支持 errors.Is(err,domain.ErrNotFound) 这类的判断 如果Cause 是 pgx.ErrNoRows 则认为是ErrNotFound
func (e *SQLError) Is(target error) bool {
	if target == domain.ErrNotFound {
		return errors.Is(e.Cause, pgx.ErrNoRows) || errors.Is(e.Cause, domain.ErrNotFound)
	}
	return errors.Is(e.Cause, target)
}

// wrapSQLError 将数据库错误包装为 SQLError 附带操作名称，SQL,和参数，
// 如果是pgx.ErrNoRows 则直接返回 domain.ErrNotFound （保持原有语意）
// 如果是 domain.ErrNotFound 则原样返回
func wrapSQLError(operation, query string, args []any, err error) error {
	
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if errors.Is(err, domain.ErrNotFound) {
		return err
	}
	
	se := &SQLError{
		Operation: operation,
		Query:     truncateQuery(query),
		Args:      formatArgs(args),
		Cause:     err,
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		se.SQLState = pgErr.SQLState()
		se.SQLCode = pgErr.Code
		se.Detail = pgErr.Detail
		se.Routine = pgErr.Routine
	}
	return se
}

// truncateQuery 截断过长的SQL
func truncateQuery(query string) string {
	query = strings.TrimSpace(query)
	if len(query) > maxQueryLogLen {
		return query
	}
	return query[:maxQueryLogLen] + "...(truncated)"
}

// formatArgs 将参数转为摘要字符串，只是出参数个数和类型概览，不输出真实值,避免泄露 payload 中的敏感数据
func formatArgs(args []any) string {
	if len(args) == 0 {
		return ""
	}
	parts := make([]string, 0, len(args))
	for i, a := range args {
		switch v := a.(type) {
		case string:
			parts = append(parts, fmt.Sprintf("$%d=string(%d)", i+1, len(v)))
		case []byte:
			parts = append(parts, fmt.Sprintf("$%d=bytes(%s)", i+1, string(v)))
		default:
			parts = append(parts, fmt.Sprintf("$%d=%T", i+1, a))
		}
	}
	return strings.Join(parts, " ")
}

func mapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	return err
}
