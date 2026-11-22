package policy

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

type Match struct {
	Subject  string `json:"subject"`
	Action   string `json:"action"`
	Resource string `json:"resource"`
}

func (m Match) Valid() bool {
	for _, value := range []string{m.Subject, m.Action, m.Resource} {
		if strings.TrimSpace(value) == "" || len(value) > 200 || strings.ContainsRune(value, '\x00') {
			return false
		}
	}
	return true
}

type Rule struct {
	ID int64 `json:"id,omitempty"`
	Match
	Effect string `json:"effect"`
}

func (r Rule) Valid() bool { return r.Match.Valid() && (r.Effect == "allow" || r.Effect == "deny") }

func Open() (*sql.DB, error) {
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

type Store struct{ DB *sql.DB }

const columns = "id, subject, action, resource, effect"

type scanner interface{ Scan(...any) error }

func scan(row scanner) (Rule, error) {
	var r Rule
	err := row.Scan(&r.ID, &r.Subject, &r.Action, &r.Resource, &r.Effect)
	return r, err
}

func (s Store) Create(ctx context.Context, r Rule) (Rule, error) {
	return scan(s.DB.QueryRowContext(ctx, "INSERT INTO policies(subject,action,resource,effect) VALUES($1,$2,$3,$4) RETURNING "+columns, r.Subject, r.Action, r.Resource, r.Effect))
}

func (s Store) Get(ctx context.Context, id int64) (Rule, error) {
	return scan(s.DB.QueryRowContext(ctx, "SELECT "+columns+" FROM policies WHERE id=$1", id))
}

func (s Store) List(ctx context.Context, after int64) ([]Rule, error) {
	rows, err := s.DB.QueryContext(ctx, "SELECT "+columns+" FROM policies WHERE id>$1 ORDER BY id LIMIT 100", after)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]Rule, 0)
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		rules = append(rules, r)
	}
	return rules, rows.Err()
}

func (s Store) Update(ctx context.Context, id int64, r Rule) (Rule, error) {
	return scan(s.DB.QueryRowContext(ctx, "UPDATE policies SET subject=$1,action=$2,resource=$3,effect=$4 WHERE id=$5 RETURNING "+columns, r.Subject, r.Action, r.Resource, r.Effect, id))
}

func (s Store) Delete(ctx context.Context, id int64) error {
	result, err := s.DB.ExecContext(ctx, "DELETE FROM policies WHERE id=$1", id)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err == nil && count == 0 {
		return sql.ErrNoRows
	}
	return err
}

func (s Store) Effect(ctx context.Context, m Match) (string, error) {
	var effect string
	err := s.DB.QueryRowContext(ctx, "SELECT effect FROM policies WHERE subject=$1 AND action=$2 AND resource=$3", m.Subject, m.Action, m.Resource).Scan(&effect)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return effect, err
}
