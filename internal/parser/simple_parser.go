package parser

import (
	"fmt"
	"github.com/tomarrell/lbadd/internal/parser/ast"
	"github.com/tomarrell/lbadd/internal/parser/scanner"
	"github.com/tomarrell/lbadd/internal/parser/scanner/token"
)

type errorReporter struct {
	p      *simpleParser
	errs   []error
	sealed bool
}

func (r *errorReporter) errorToken(t token.Token) {
	r.errorf("%w: %s", ErrScanner, t.Value())
}

func (r *errorReporter) incompleteStatement() {
	next, ok := r.p.unsafeLowLevelLookahead()
	if !ok {
		r.errorf("%w: EOF", ErrIncompleteStatement)
	} else {
		r.errorf("%w: %s at (%d:%d) offset %d length %d", ErrIncompleteStatement, next.Type().String(), next.Line(), next.Col(), next.Offset(), next.Length())
	}
}

func (r *errorReporter) prematureEOF() {
	r.errorf("%w", ErrPrematureEOF)
	r.sealed = true
}

func (r *errorReporter) unexpectedToken(expected ...token.Type) {
	if r.sealed {
		return
	}
	next, ok := r.p.unsafeLowLevelLookahead()
	if !ok || next.Type() == token.EOF {
		// use this instead of r.prematureEOF() because we can add the
		// information about what tokens were expected
		r.errorf("%w: expected %s", ErrPrematureEOF, expected)
		r.sealed = true
		return
	}

	r.errorf("%w: got %s but expected one of %s at (%d:%d) offset %d length %d", ErrUnexpectedToken, next, expected, next.Line(), next.Col(), next.Offset(), next.Length())
}

func (r *errorReporter) unhandledToken(t token.Token) {
	r.errorf("%w: %s(%s) at (%d:%d) offset %d lenght %d", ErrUnknownToken, t.Type().String(), t.Value(), t.Line(), t.Col(), t.Offset(), t.Length())
}

func (r *errorReporter) unsupportedConstruct(t token.Token) {
	r.errorf("%w: %s(%s) at (%d:%d) offset %d lenght %d", ErrUnsupportedConstruct, t.Type().String(), t.Value(), t.Line(), t.Col(), t.Offset(), t.Length())
}

func (r *errorReporter) errorf(format string, args ...interface{}) {
	r.errs = append(r.errs, fmt.Errorf(format, args...))
}

type reporter interface {
	errorToken(t token.Token)
	incompleteStatement()
	prematureEOF()
	unexpectedToken(expected ...token.Type)
	unhandledToken(t token.Token)
	unsupportedConstruct(t token.Token)
}

var _ Parser = (*simpleParser)(nil) // ensure that simpleParser implements Parser

type simpleParser struct {
	scanner scanner.Scanner
}

// New creates new ready to use parser.
func New(input string) Parser {
	return &simpleParser{
		scanner: scanner.New([]rune(input)),
	}
}

func (p *simpleParser) Next() (*ast.SQLStmt, []error, bool) {
	if !p.scanner.HasNext() {
		return nil, []error{}, false
	}
	errs := &errorReporter{
		p:    p,
		errs: []error{},
	}
	stmt := p.parseSQLStatement(errs)
	return stmt, errs.errs, true
}

// searchNext skips tokens until a token is of one of the given types. That
// token will not be consumed, every other token will be consumed and an
// unexpected token error will be reported.
func (p *simpleParser) searchNext(r reporter, types ...token.Type) {
	for {
		next, ok := p.unsafeLowLevelLookahead()
		if !ok {
			return
		}
		for _, typ := range types {
			if next.Type() == typ {
				return
			}
		}
		r.unexpectedToken(types...)
		p.consumeToken()
	}
}

func (p *simpleParser) skipUntil(types ...token.Type) {
	for {
		next, ok := p.unsafeLowLevelLookahead()
		if !ok {
			return
		}
		for _, typ := range types {
			if next.Type() == typ {
				return
			}
		}
		p.consumeToken()
	}
}

// unsafeLowLevelLookahead is a low level lookahead, only use if needed.
// Remember to check for token.Error, token.EOF and token.StatementSeparator, as
// this will only return hasNext=false if there are no more tokens (which only
// should occur after an EOF token). Any other token will be returned with
// next=<token>,hasNext=true.
func (p *simpleParser) unsafeLowLevelLookahead() (next token.Token, hasNext bool) {
	if !p.scanner.HasNext() {
		return nil, false
	}

	return p.scanner.Peek(), true
}

func (p *simpleParser) lookaheadWithType(r reporter, typ token.Type) (token.Token, bool) {
	next, hasNext := p.lookahead(r)
	return next, hasNext && next.Type() == typ
}

// lookahead performs a lookahead while consuming any error or statement
// separator token, and reports an EOF, Error or IncompleteStatement if
// appropriate. If this returns ok=false, return from your parse function
// without reporting any more errors. If ok=false, this means that the next
// token was either a StatementSeparator or EOF, and an error has been reported.
func (p *simpleParser) lookahead(r reporter) (next token.Token, ok bool) {
	next, ok = p.unsafeLowLevelLookahead()

	// drain all error tokens
	for ok && next.Type() == token.Error {
		r.errorToken(next)
		p.consumeToken()
	}

	if !ok || next.Type() == token.EOF {
		r.prematureEOF()
		ok = false
	} else if next.Type() == token.StatementSeparator {
		r.incompleteStatement()
		ok = false
	}
	return
}

func (p *simpleParser) consumeToken() {
	_ = p.scanner.Next()
}

func (p *simpleParser) parseSQLStatement(r reporter) (stmt *ast.SQLStmt) {
	stmt = &ast.SQLStmt{}

	if next, ok := p.lookaheadWithType(r, token.KeywordExplain); ok {
		stmt.Explain = next
		p.consumeToken()

		if next, ok := p.lookaheadWithType(r, token.KeywordQuery); ok {
			stmt.Query = next
			p.consumeToken()

			if next, ok := p.lookaheadWithType(r, token.KeywordPlan); ok {
				stmt.Plan = next
				p.consumeToken()
			} else {
				r.unexpectedToken(token.KeywordPlan)
				// At this point, just assume that 'QUERY' was a mistake. Don't
				// abort. It's very unlikely that 'PLAN' occurs somewhere, so
				// assume that the user meant to input 'EXPLAIN <statement>'
				// instead of 'EXPLAIN QUERY PLAN <statement>'.
			}
		}
	}

	// according to the grammar, these are the tokens that initiate a statement
	p.searchNext(r, token.StatementSeparator, token.EOF, token.KeywordAlter, token.KeywordAnalyze, token.KeywordAttach, token.KeywordBegin, token.KeywordCommit, token.KeywordCreate, token.KeywordDelete, token.KeywordDetach, token.KeywordDrop, token.KeywordInsert, token.KeywordPragma, token.KeywordReindex, token.KeywordRelease, token.KeywordRollback, token.KeywordSavepoint, token.KeywordSelect, token.KeywordUpdate, token.KeywordVacuum)

	next, ok := p.unsafeLowLevelLookahead()
	if !ok {
		r.incompleteStatement()
		return
	}

	// lookahead processing to check what the statement ahead is
	switch next.Type() {
	case token.KeywordAlter:
		stmt.AlterTableStmt = p.parseAlterTableStmt(r)
	case token.StatementSeparator:
		r.incompleteStatement()
		p.consumeToken()
	case token.KeywordPragma:
		// we don't support pragmas, as we don't need them yet
		r.unsupportedConstruct(next)
		p.skipUntil(token.StatementSeparator, token.EOF)
	default:
		r.unsupportedConstruct(next)
		p.skipUntil(token.StatementSeparator, token.EOF)
	}

	p.searchNext(r, token.StatementSeparator, token.EOF)
	next, ok = p.lookahead(r)
	if !ok {
		return
	}
	if next.Type() == token.StatementSeparator {
		// if there's a statement separator, consume this token and get the next
		// token, so that it can be checked if that next token is an EOF
		p.consumeToken()

		next, ok = p.lookahead(r)
		if !ok {
			return
		}
	}
	if next.Type() == token.EOF {
		p.consumeToken()
		return
	}
	return
}

func (p *simpleParser) parseAlterTableStmt(r reporter) (stmt *ast.AlterTableStmt) {
	stmt = &ast.AlterTableStmt{}

	p.searchNext(r, token.KeywordAlter)
	next, ok := p.lookahead(r)
	if !ok {
		return
	}
	stmt.Alter = next
	p.consumeToken()

	next, ok = p.lookahead(r)
	if !ok {
		return
	}
	if next.Type() == token.KeywordTable {
		stmt.Table = next
		p.consumeToken()
	} else {
		r.unexpectedToken(token.KeywordTable)
		// do not consume anything, report that there is no 'TABLE' keyword, and
		// proceed as if we would have received it
	}

	schemaOrTableName, ok := p.lookahead(r)
	if !ok {
		return
	}
	if schemaOrTableName.Type() != token.Literal {
		r.unexpectedToken(token.Literal)
		return
	}
	p.consumeToken() // consume the literal

	next, ok = p.lookahead(r)
	if !ok {
		return
	}
	if next.Value() == "." {
		// schemaOrTableName is schema name
		stmt.SchemaName = schemaOrTableName
		stmt.Period = next
		p.consumeToken()

		tableName, ok := p.lookahead(r)
		if !ok {
			return
		}
		if tableName.Type() == token.Literal {
			stmt.TableName = tableName
			p.consumeToken()
		}
	} else {
		stmt.TableName = schemaOrTableName
	}
	switch next.Type() {
	case token.KeywordRename:
		stmt.Rename = next
		p.consumeToken()

		next, ok = p.lookahead(r)
		if !ok {
			return
		}
		switch next.Type() {
		case token.KeywordTo:
			stmt.To = next
			p.consumeToken()

			next, ok = p.lookahead(r)
			if !ok {
				return
			}
			if next.Type() != token.Literal {
				r.unexpectedToken(token.Literal)
				p.consumeToken()
				return
			}
			stmt.NewTableName = next
			p.consumeToken()
		case token.KeywordColumn:
			stmt.Column = next
			p.consumeToken()

			next, ok = p.lookahead(r)
			if !ok {
				return
			}
			if next.Type() != token.Literal {
				r.unexpectedToken(token.Literal)
				p.consumeToken()
				return
			}

			fallthrough
		case token.Literal:
			stmt.ColumnName = next
			p.consumeToken()

			next, ok = p.lookahead(r)
			if !ok {
				return
			}
			if next.Type() != token.KeywordTo {
				r.unexpectedToken(token.KeywordTo)
				p.consumeToken()
				return
			}

			stmt.To = next
			p.consumeToken()

			next, ok = p.lookahead(r)
			if !ok {
				return
			}
			if next.Type() != token.Literal {
				r.unexpectedToken(token.Literal)
				p.consumeToken()
				return
			}

			stmt.NewColumnName = next
			p.consumeToken()
		default:
			r.unexpectedToken(token.KeywordTo, token.KeywordColumn, token.Literal)
		}
	case token.KeywordAdd:
		stmt.Add = next
		p.consumeToken()

		next, ok = p.lookahead(r)
		if !ok {
			return
		}
		switch next.Type() {
		case token.KeywordColumn:
			stmt.Column = next
			p.consumeToken()

			next, ok = p.lookahead(r)
			if !ok {
				return
			}
			if next.Type() != token.Literal {
				r.unexpectedToken(token.Literal)
				p.consumeToken()
				return
			}
			fallthrough
		case token.Literal:
			stmt.ColumnDef = p.parseColumnDef(r)
		default:
			r.unexpectedToken(token.KeywordColumn, token.Literal)
		}
	}

	return
}

func (p *simpleParser) parseColumnDef(r reporter) (def *ast.ColumnDef) {
	panic("implement me")
}
