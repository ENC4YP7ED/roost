package store

import (
	"database/sql"
	"errors"
)

const scheduleCols = `id, server_id, name, cron_day_of_week, cron_month, cron_day_of_month,
	cron_hour, cron_minute, is_active, is_processing, only_when_online, last_run_at,
	next_run_at, created_at, updated_at`

func scanSchedule(row interface{ Scan(...any) error }) (*Schedule, error) {
	v := &Schedule{}
	err := row.Scan(&v.ID, &v.ServerID, &v.Name, &v.CronDayOfWeek, &v.CronMonth,
		&v.CronDayOfMonth, &v.CronHour, &v.CronMinute, &v.IsActive, &v.IsProcessing,
		&v.OnlyWhenOnline, &v.LastRunAt, &v.NextRunAt, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) SchedulesForServer(serverID int64) ([]*Schedule, error) {
	rows, err := s.db.Query(`SELECT `+scheduleCols+` FROM schedules WHERE server_id = ? ORDER BY id`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Schedule
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// DueSchedules returns active, unprocessed schedules whose next run time has
// passed.
func (s *Store) DueSchedules() ([]*Schedule, error) {
	rows, err := s.db.Query(`SELECT `+scheduleCols+` FROM schedules
		WHERE is_active = 1 AND is_processing = 0 AND next_run_at IS NOT NULL AND next_run_at <= ?`, now())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Schedule
	for rows.Next() {
		v, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) ScheduleByID(id int64) (*Schedule, error) {
	return scanSchedule(s.db.QueryRow(`SELECT `+scheduleCols+` FROM schedules WHERE id = ?`, id))
}

func (s *Store) CreateSchedule(v *Schedule) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO schedules (server_id, name, cron_day_of_week, cron_month,
		cron_day_of_month, cron_hour, cron_minute, is_active, is_processing, only_when_online,
		last_run_at, next_run_at, created_at, updated_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ServerID, v.Name, v.CronDayOfWeek, v.CronMonth, v.CronDayOfMonth, v.CronHour,
		v.CronMinute, v.IsActive, v.IsProcessing, v.OnlyWhenOnline, v.LastRunAt, v.NextRunAt, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateSchedule(v *Schedule) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE schedules SET name=?, cron_day_of_week=?, cron_month=?,
		cron_day_of_month=?, cron_hour=?, cron_minute=?, is_active=?, is_processing=?,
		only_when_online=?, last_run_at=?, next_run_at=?, updated_at=? WHERE id=?`,
		v.Name, v.CronDayOfWeek, v.CronMonth, v.CronDayOfMonth, v.CronHour, v.CronMinute,
		v.IsActive, v.IsProcessing, v.OnlyWhenOnline, v.LastRunAt, v.NextRunAt, v.UpdatedAt, v.ID)
	return err
}

func (s *Store) DeleteSchedule(id int64) error {
	_, err := s.db.Exec(`DELETE FROM schedules WHERE id = ?`, id)
	return err
}

// ---- schedule tasks ----

const taskCols = `id, schedule_id, sequence_id, action, payload, time_offset, is_queued,
	continue_on_failure, created_at, updated_at`

func scanTask(row interface{ Scan(...any) error }) (*ScheduleTask, error) {
	v := &ScheduleTask{}
	err := row.Scan(&v.ID, &v.ScheduleID, &v.SequenceID, &v.Action, &v.Payload, &v.TimeOffset,
		&v.IsQueued, &v.ContinueOnFailure, &v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) TasksForSchedule(scheduleID int64) ([]*ScheduleTask, error) {
	rows, err := s.db.Query(`SELECT `+taskCols+` FROM schedule_tasks WHERE schedule_id = ? ORDER BY sequence_id`, scheduleID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*ScheduleTask
	for rows.Next() {
		v, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) TaskByID(id int64) (*ScheduleTask, error) {
	return scanTask(s.db.QueryRow(`SELECT `+taskCols+` FROM schedule_tasks WHERE id = ?`, id))
}

func (s *Store) CreateTask(v *ScheduleTask) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO schedule_tasks (schedule_id, sequence_id, action, payload,
		time_offset, is_queued, continue_on_failure, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		v.ScheduleID, v.SequenceID, v.Action, v.Payload, v.TimeOffset, v.IsQueued,
		v.ContinueOnFailure, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateTask(v *ScheduleTask) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE schedule_tasks SET sequence_id=?, action=?, payload=?,
		time_offset=?, is_queued=?, continue_on_failure=?, updated_at=? WHERE id=?`,
		v.SequenceID, v.Action, v.Payload, v.TimeOffset, v.IsQueued, v.ContinueOnFailure,
		v.UpdatedAt, v.ID)
	return err
}

func (s *Store) DeleteTask(id int64) error {
	_, err := s.db.Exec(`DELETE FROM schedule_tasks WHERE id = ?`, id)
	return err
}

// ---- backups ----

const backupCols = `id, server_id, uuid, upload_id, is_successful, is_locked, name,
	ignored_files, disk, checksum, bytes, completed_at, deleted_at, created_at, updated_at`

func scanBackup(row interface{ Scan(...any) error }) (*Backup, error) {
	v := &Backup{}
	err := row.Scan(&v.ID, &v.ServerID, &v.UUID, &v.UploadID, &v.IsSuccessful, &v.IsLocked,
		&v.Name, &v.IgnoredFiles, &v.Disk, &v.Checksum, &v.Bytes, &v.CompletedAt, &v.DeletedAt,
		&v.CreatedAt, &v.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	return v, err
}

func (s *Store) BackupsForServer(serverID int64) ([]*Backup, error) {
	rows, err := s.db.Query(`SELECT `+backupCols+` FROM backups
		WHERE server_id = ? AND deleted_at IS NULL ORDER BY id DESC`, serverID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Backup
	for rows.Next() {
		v, err := scanBackup(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (s *Store) CountBackupsForServer(serverID int64) (int64, error) {
	var n int64
	err := s.db.QueryRow(`SELECT COUNT(*) FROM backups WHERE server_id = ? AND deleted_at IS NULL`, serverID).Scan(&n)
	return n, err
}

func (s *Store) BackupByUUID(uuid string) (*Backup, error) {
	return scanBackup(s.db.QueryRow(`SELECT `+backupCols+` FROM backups WHERE uuid = ?`, uuid))
}

func (s *Store) CreateBackup(v *Backup) error {
	ts := now()
	v.CreatedAt, v.UpdatedAt = ts, ts
	res, err := s.db.Exec(`INSERT INTO backups (server_id, uuid, upload_id, is_successful,
		is_locked, name, ignored_files, disk, checksum, bytes, completed_at, created_at, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		v.ServerID, v.UUID, v.UploadID, v.IsSuccessful, v.IsLocked, v.Name, v.IgnoredFiles,
		v.Disk, v.Checksum, v.Bytes, v.CompletedAt, ts, ts)
	if err != nil {
		return err
	}
	v.ID, _ = res.LastInsertId()
	return nil
}

func (s *Store) UpdateBackup(v *Backup) error {
	v.UpdatedAt = now()
	_, err := s.db.Exec(`UPDATE backups SET upload_id=?, is_successful=?, is_locked=?,
		checksum=?, bytes=?, completed_at=?, deleted_at=?, updated_at=? WHERE id=?`,
		v.UploadID, v.IsSuccessful, v.IsLocked, v.Checksum, v.Bytes, v.CompletedAt,
		v.DeletedAt, v.UpdatedAt, v.ID)
	return err
}
