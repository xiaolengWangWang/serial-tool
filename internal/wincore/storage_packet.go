package wincore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

func migratePacketColumns(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(received_data)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, kind string
		var fallback any
		if err := rows.Scan(&cid, &name, &kind, &notnull, &fallback, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[name] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, name := range []string{"packet_id", "direction", "transport", "connection_id", "endpoint", "leg", "session_key"} {
		if !columns[name] {
			if _, err := db.Exec("ALTER TABLE received_data ADD COLUMN " + name + " TEXT NOT NULL DEFAULT ''"); err != nil {
				return fmt.Errorf("packet migration %s: %w", name, err)
			}
		}
	}
	return nil
}

func (s *Store) SetCaptureKey(key string) { s.Lock(); s.captureKey = key; s.Unlock() }

// Record stores immutable capture metadata; an old reader cannot enter a replacement session.
func (s *Store) Record(p Packet) error {
	s.Lock()
	defer s.Unlock()
	if s.db == nil || s.sessionID == 0 || (p.SessionKey != "" && p.SessionKey != s.captureKey) {
		return nil
	}
	if p.Timestamp.IsZero() {
		p.Timestamp = time.Now()
	}
	if err := s.rotateIfNeededLocked(p.Timestamp, int64(len(p.Data))); err != nil {
		return err
	}
	_, err := s.db.Exec(`INSERT INTO received_data(session_id,received_at,source,size_bytes,raw_data,text_data,is_utf8,packet_id,direction,transport,connection_id,endpoint,leg,session_key) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, s.sessionID, p.Timestamp.Format(time.RFC3339Nano), p.Source, len(p.Data), p.Data, strings.ToValidUTF8(string(p.Data), "�"), utf8.Valid(p.Data), p.ID, p.Direction, p.Transport, p.ConnectionID, p.Endpoint, p.Leg, p.SessionKey)
	return err
}
