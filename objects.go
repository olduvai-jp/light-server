package main

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"strings"
	"time"
)

type object struct {
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data"`
	CreatedAt string          `json:"created_at"`
	UpdatedAt string          `json:"updated_at"`
}
type filter struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value any    `json:"value"`
}
type search struct {
	Filters []filter `json:"filters"`
	Sort    string   `json:"sort"`
	Desc    bool     `json:"desc"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

func getObject(db *sql.DB, c, id string) (object, error) {
	var o object
	var data string
	e := db.QueryRow("SELECT id,data,created_at,updated_at FROM objects WHERE collection=? AND id=?", c, id).Scan(&o.ID, &data, &o.CreatedAt, &o.UpdatedAt)
	o.Data = json.RawMessage(data)
	return o, e
}
func (s *Server) objects(w http.ResponseWriter, r *http.Request, p *Project, tail []string) {
	if len(tail) == 0 && r.Method == "GET" {
		rows, e := p.db.Query("SELECT DISTINCT collection FROM objects ORDER BY collection")
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		defer rows.Close()
		names := []string{}
		for rows.Next() {
			var n string
			if e = rows.Scan(&n); e != nil {
				fail(w, 500, e.Error())
				return
			}
			names = append(names, n)
		}
		if e = rows.Err(); e != nil {
			fail(w, 500, e.Error())
			return
		}
		reply(w, 200, names)
		return
	}
	if len(tail) < 2 || !validName.MatchString(tail[0]) {
		fail(w, 400, "不正なコレクション")
		return
	}
	c := tail[0]
	if tail[1] == "search" && len(tail) == 2 && r.Method == "POST" {
		q := search{Limit: 100}
		if e := decode(w, r, &q); e != nil {
			fail(w, 400, e.Error())
			return
		}
		s.query(w, p, c, q)
		return
	}
	if tail[1] != "objects" || len(tail) > 3 {
		fail(w, 404, "見つかりません")
		return
	}
	id := ""
	if len(tail) == 3 {
		id = tail[2]
		if !validName.MatchString(id) {
			fail(w, 400, "不正なID")
			return
		}
	}
	if id == "" && r.Method == "GET" {
		l, o, e := page(r)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		desc := r.URL.Query().Get("desc")
		if desc != "" && desc != "true" && desc != "false" {
			fail(w, 400, "descはtrueまたはfalse")
			return
		}
		s.query(w, p, c, search{Limit: l, Offset: o, Sort: r.URL.Query().Get("sort"), Desc: desc == "true"})
		return
	}
	if (id == "" && r.Method == "POST") || (id != "" && r.Method == "PUT") {
		var data map[string]any
		if e := decode(w, r, &data); e != nil || data == nil {
			fail(w, 400, "JSONオブジェクトが必要です")
			return
		}
		b, e := json.Marshal(data)
		if e != nil {
			fail(w, 400, e.Error())
			return
		}
		status := 200
		if id == "" {
			id = uuid.NewString()
			status = 201
		}
		now := time.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")
		_, e = p.db.Exec(`INSERT INTO objects(collection,id,data,created_at,updated_at) VALUES(?,?,?,?,?) ON CONFLICT(collection,id) DO UPDATE SET data=excluded.data,updated_at=excluded.updated_at`, c, id, string(b), now, now)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		o, e := getObject(p.db, c, id)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		reply(w, status, o)
		return
	}
	if id != "" && r.Method == "GET" {
		o, e := getObject(p.db, c, id)
		if errors.Is(e, sql.ErrNoRows) {
			fail(w, 404, "見つかりません")
			return
		}
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		reply(w, 200, o)
		return
	}
	if id != "" && r.Method == "DELETE" {
		res, e := p.db.Exec("DELETE FROM objects WHERE collection=? AND id=?", c, id)
		if e != nil {
			fail(w, 500, e.Error())
			return
		}
		n, _ := res.RowsAffected()
		if n == 0 {
			fail(w, 404, "見つかりません")
			return
		}
		reply(w, 204, nil)
		return
	}
	fail(w, 405, "未対応")
}
func (s *Server) query(w http.ResponseWriter, p *Project, c string, q search) {
	if q.Limit < 1 || q.Limit > 1000 || q.Offset < 0 || len(q.Filters) > 32 {
		fail(w, 400, "limitは1..1000、offsetは0以上、条件は32個まで")
		return
	}
	where := []string{"collection=?"}
	args := []any{c}
	for _, f := range q.Filters {
		if f.Field == "" || len(f.Field) > 256 {
			fail(w, 400, "検索項目が不正です")
			return
		}
		cond := ""
		values := []any{f.Field}
		if f.Op == "contains" {
			v, ok := f.Value.(string)
			if !ok {
				fail(w, 400, "containsは文字列が必要です")
				return
			}
			cond = "j.type='text' AND instr(j.value,?)>0"
			values = append(values, v)
		} else if f.Op == "eq" {
			switch v := f.Value.(type) {
			case nil:
				cond = "j.type='null'"
			case bool:
				if v {
					cond = "j.type='true'"
				} else {
					cond = "j.type='false'"
				}
			case string:
				cond = "j.type='text' AND j.value=?"
				values = append(values, v)
			case json.Number:
				cond = "j.type IN ('integer','real') AND j.value=CAST(? AS NUMERIC)"
				values = append(values, string(v))
			default:
				fail(w, 400, "eqはnull・真偽値・数値・文字列に対応")
				return
			}
		} else {
			fail(w, 400, "opはeqまたはcontains")
			return
		}
		where = append(where, "EXISTS (SELECT 1 FROM json_each(objects.data) j WHERE j.key=? AND "+cond+")")
		args = append(args, values...)
	}
	order := "id"
	if q.Sort != "" {
		switch q.Sort {
		case "$id":
			order = "id"
		case "$created_at":
			order = "created_at"
		case "$updated_at":
			order = "updated_at"
		default:
			if len(q.Sort) > 256 {
				fail(w, 400, "並べ替え項目が長すぎます")
				return
			}
			order = "(SELECT value FROM json_each(objects.data) WHERE key=? LIMIT 1)"
			args = append(args, q.Sort)
		}
	}
	direction := " ASC"
	if q.Desc {
		direction = " DESC"
	}
	query := "SELECT id,data,created_at,updated_at FROM objects WHERE " + strings.Join(where, " AND ") + " ORDER BY " + order + direction + ",id ASC LIMIT ? OFFSET ?"
	args = append(args, q.Limit, q.Offset)
	rows, e := p.db.Query(query, args...)
	if e != nil {
		fail(w, 500, e.Error())
		return
	}
	defer rows.Close()
	items := []object{}
	for rows.Next() {
		var o object
		var b string
		if e = rows.Scan(&o.ID, &b, &o.CreatedAt, &o.UpdatedAt); e != nil {
			fail(w, 500, e.Error())
			return
		}
		o.Data = json.RawMessage(b)
		items = append(items, o)
	}
	if e = rows.Err(); e != nil {
		fail(w, 500, e.Error())
		return
	}
	reply(w, 200, map[string]any{"items": items, "limit": q.Limit, "offset": q.Offset})
}
