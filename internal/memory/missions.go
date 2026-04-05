package memory

import (
	"context"
	"database/sql"
	"time"
)

type Mission struct {
	ID          string
	Type        string
	Target      string
	Status      string
	AssignedAt  time.Time
	CompletedAt time.Time
	Result      string
}

func (m *Memory) CreateMissionTable() error {
	schema := `
	CREATE TABLE IF NOT EXISTS missions (
		id VARCHAR PRIMARY KEY,
		type VARCHAR,
		target TEXT,
		status VARCHAR DEFAULT 'pending',
		assigned_at TIMESTAMP DEFAULT NOW(),
		completed_at TIMESTAMP,
		result TEXT
	);

	CREATE TABLE IF NOT EXISTS processed_dms (
		id VARCHAR PRIMARY KEY,
		processed_at TIMESTAMP DEFAULT NOW()
	);
	`
	_, err := m.db.Exec(schema)
	return err
}

func (m *Memory) CreateMission(ctx context.Context, mission Mission) error {
	query := `
	INSERT INTO missions (id, type, target, status, assigned_at)
	VALUES (?, ?, ?, ?, ?)
	`
	_, err := m.db.ExecContext(ctx, query,
		mission.ID, mission.Type, mission.Target, mission.Status, mission.AssignedAt)
	return err
}

func (m *Memory) GetPendingMissions(ctx context.Context) ([]Mission, error) {
	query := `
	SELECT id, type, target, status, assigned_at, completed_at, result
	FROM missions 
	WHERE status = 'pending'
	ORDER BY assigned_at ASC
	`
	rows, err := m.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var missions []Mission
	for rows.Next() {
		var m Mission
		var completedAt sql.NullTime
		var result sql.NullString
		if err := rows.Scan(&m.ID, &m.Type, &m.Target, &m.Status, &m.AssignedAt, &completedAt, &result); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			m.CompletedAt = completedAt.Time
		}
		if result.Valid {
			m.Result = result.String
		}
		missions = append(missions, m)
	}
	return missions, nil
}

func (m *Memory) CompleteMission(ctx context.Context, id, result string) error {
	query := `
	UPDATE missions SET 
		status = 'completed',
		completed_at = NOW(),
		result = ?
	WHERE id = ?
	`
	_, err := m.db.ExecContext(ctx, query, result, id)
	return err
}

func (m *Memory) MarkDMProcessed(ctx context.Context, id string) error {
	query := `INSERT INTO processed_dms (id) VALUES (?) ON CONFLICT DO NOTHING`
	_, err := m.db.ExecContext(ctx, query, id)
	return err
}

func (m *Memory) IsDMProcessed(ctx context.Context, id string) (bool, error) {
	query := `SELECT 1 FROM processed_dms WHERE id = ?`
	var exists int
	err := m.db.QueryRowContext(ctx, query, id).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
