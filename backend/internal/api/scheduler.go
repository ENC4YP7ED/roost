package api

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"roost/internal/store"
	"roost/internal/wings"
)

// ---- schedule CRUD helpers ----

func (a *API) upsertSchedule(w http.ResponseWriter, r *http.Request, sc *store.Schedule) {
	srv := serverFrom(r)
	var body struct {
		Name           string `json:"name"`
		Minute         string `json:"minute"`
		Hour           string `json:"hour"`
		DayOfMonth     string `json:"day_of_month"`
		Month          string `json:"month"`
		DayOfWeek      string `json:"day_of_week"`
		IsActive       *bool  `json:"is_active"`
		OnlyWhenOnline bool   `json:"only_when_online"`
	}
	if err := decode(r, &body); err != nil || body.Name == "" {
		writeError(w, http.StatusUnprocessableEntity, "A schedule name must be provided.")
		return
	}
	def := func(v, fallback string) string {
		if strings.TrimSpace(v) == "" {
			return fallback
		}
		return strings.TrimSpace(v)
	}
	isNew := sc == nil
	if isNew {
		sc = &store.Schedule{ServerID: srv.ID, IsActive: true}
	}
	sc.Name = body.Name
	sc.CronMinute = def(body.Minute, "*/5")
	sc.CronHour = def(body.Hour, "*")
	sc.CronDayOfMonth = def(body.DayOfMonth, "*")
	sc.CronMonth = def(body.Month, "*")
	sc.CronDayOfWeek = def(body.DayOfWeek, "*")
	if body.IsActive != nil {
		sc.IsActive = *body.IsActive
	}
	sc.OnlyWhenOnline = body.OnlyWhenOnline
	if next, ok := nextCronRun(sc, time.Now()); ok {
		ts := next.UTC().Format(time.RFC3339)
		sc.NextRunAt = &ts
	} else {
		writeError(w, http.StatusUnprocessableEntity, "The cron expression provided is not valid.")
		return
	}
	var err error
	if isNew {
		err = a.Store.CreateSchedule(sc)
		a.activity(r, "server:schedule.create", map[string]any{"name": sc.Name}, [2]any{"server", srv.ID})
	} else {
		err = a.Store.UpdateSchedule(sc)
		a.activity(r, "server:schedule.update", map[string]any{"name": sc.Name}, [2]any{"server", srv.ID})
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	tasks, _ := a.Store.TasksForSchedule(sc.ID)
	writeItem(w, http.StatusOK, "server_schedule", trSchedule(sc, tasks))
}

func (a *API) upsertTask(w http.ResponseWriter, r *http.Request, t *store.ScheduleTask) {
	srv := serverFrom(r)
	scheduleID := parseID(r, "schedule")
	sc, err := a.Store.ScheduleByID(scheduleID)
	if err != nil || sc.ServerID != srv.ID {
		writeError(w, http.StatusNotFound, "Schedule not found.")
		return
	}
	var body struct {
		Action            string `json:"action"`
		Payload           string `json:"payload"`
		TimeOffset        int64  `json:"time_offset"`
		ContinueOnFailure bool   `json:"continue_on_failure"`
	}
	if err := decode(r, &body); err != nil {
		writeError(w, http.StatusUnprocessableEntity, "Invalid request body.")
		return
	}
	switch body.Action {
	case "command", "power", "backup":
	default:
		writeError(w, http.StatusUnprocessableEntity, "Action must be one of command, power, backup.")
		return
	}
	if body.Action != "backup" && body.Payload == "" {
		writeError(w, http.StatusUnprocessableEntity, "A payload must be provided for this action.")
		return
	}
	if t == nil {
		existing, _ := a.Store.TasksForSchedule(sc.ID)
		t = &store.ScheduleTask{ScheduleID: sc.ID, SequenceID: int64(len(existing) + 1)}
	} else if t.ScheduleID != sc.ID {
		writeError(w, http.StatusNotFound, "Task not found.")
		return
	}
	t.Action = body.Action
	t.Payload = body.Payload
	t.TimeOffset = body.TimeOffset
	t.ContinueOnFailure = body.ContinueOnFailure
	if t.ID == 0 {
		err = a.Store.CreateTask(t)
	} else {
		err = a.Store.UpdateTask(t)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeItem(w, http.StatusOK, "schedule_task", trTask(t))
}

// ---- background processing ----

func (a *API) processSchedules() {
	due, err := a.Store.DueSchedules()
	if err != nil {
		return
	}
	for _, sc := range due {
		go a.runSchedule(sc)
	}
}

// runSchedule executes a schedule's tasks in order, honouring offsets.
func (a *API) runSchedule(sc *store.Schedule) {
	sc.IsProcessing = true
	a.Store.UpdateSchedule(sc)
	defer func() {
		ts := nowISO()
		sc.LastRunAt = &ts
		if next, ok := nextCronRun(sc, time.Now()); ok {
			n := next.UTC().Format(time.RFC3339)
			sc.NextRunAt = &n
		}
		sc.IsProcessing = false
		a.Store.UpdateSchedule(sc)
	}()

	srv, err := a.Store.ServerByID(sc.ServerID)
	if err != nil {
		return
	}
	node, err := a.Store.NodeByID(srv.NodeID)
	if err != nil {
		return
	}
	client := wings.New(node)
	tasks, _ := a.Store.TasksForSchedule(sc.ID)
	for _, t := range tasks {
		if t.TimeOffset > 0 {
			time.Sleep(time.Duration(t.TimeOffset) * time.Second)
		}
		var err error
		switch t.Action {
		case "command":
			err = client.SendCommands(srv.UUID, []string{t.Payload})
		case "power":
			err = client.SendPower(srv.UUID, t.Payload)
		case "backup":
			b := &store.Backup{ServerID: srv.ID, UUID: newUUID(), Name: "Scheduled backup: " + sc.Name, IgnoredFiles: "[]", Disk: "wings"}
			if err = a.Store.CreateBackup(b); err == nil {
				err = client.Backup(srv.UUID, map[string]any{"adapter": "wings", "uuid": b.UUID, "ignore": t.Payload})
			}
		}
		if err != nil {
			log.Printf("schedule %d task %d failed: %v", sc.ID, t.ID, err)
			if !t.ContinueOnFailure {
				return
			}
		}
	}
}

// ---- tiny cron evaluator (minute resolution) ----

// nextCronRun finds the next time after `from` matching the schedule's cron
// fields, scanning up to two years ahead.
func nextCronRun(sc *store.Schedule, from time.Time) (time.Time, bool) {
	fields := []struct {
		expr     string
		min, max int
	}{
		{sc.CronMinute, 0, 59},
		{sc.CronHour, 0, 23},
		{sc.CronDayOfMonth, 1, 31},
		{sc.CronMonth, 1, 12},
		{sc.CronDayOfWeek, 0, 6},
	}
	sets := make([]map[int]bool, 5)
	for i, f := range fields {
		set, ok := parseCronField(f.expr, f.min, f.max)
		if !ok {
			return time.Time{}, false
		}
		sets[i] = set
	}
	t := from.Truncate(time.Minute).Add(time.Minute)
	limit := from.Add(2 * 365 * 24 * time.Hour)
	for t.Before(limit) {
		if sets[3][int(t.Month())] &&
			sets[2][t.Day()] &&
			sets[4][int(t.Weekday())] &&
			sets[1][t.Hour()] &&
			sets[0][t.Minute()] {
			return t, true
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}, false
}

// parseCronField supports *, */n, lists, ranges and bare numbers.
func parseCronField(expr string, min, max int) (map[int]bool, bool) {
	set := map[int]bool{}
	expr = strings.TrimSpace(expr)
	if expr == "" {
		expr = "*"
	}
	for _, part := range strings.Split(expr, ",") {
		part = strings.TrimSpace(part)
		step := 1
		if base, s, ok := strings.Cut(part, "/"); ok {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 {
				return nil, false
			}
			step = n
			part = base
		}
		lo, hi := min, max
		switch {
		case part == "*":
		case strings.Contains(part, "-"):
			a, b, _ := strings.Cut(part, "-")
			var err1, err2 error
			lo, err1 = strconv.Atoi(a)
			hi, err2 = strconv.Atoi(b)
			if err1 != nil || err2 != nil {
				return nil, false
			}
		default:
			n, err := strconv.Atoi(part)
			if err != nil {
				return nil, false
			}
			lo, hi = n, n
		}
		if lo < min || hi > max || lo > hi {
			return nil, false
		}
		for v := lo; v <= hi; v += step {
			set[v] = true
		}
	}
	return set, len(set) > 0
}
