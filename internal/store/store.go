package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"shifttracker/internal/jalali"
	"shifttracker/internal/models"

	_ "modernc.org/sqlite"
)

const maxBackups = 60

// حداقل مدت مجاز برای یک شیفت (چه زنده، چه دستی).
// شیفت‌های کوتاه‌تر از این مقدار (مثلاً دابل‌کلیک اشتباهی روی «ورود» و «خروج») ثبت نمی‌شوند.
const minShiftDuration = time.Minute

// زمانی که یک شیفت هنوز باز است (CheckOut == nil)، برای محاسبه‌ی تداخل زمانی
// آن را به‌صورت شیفتی در نظر می‌گیریم که تا آینده‌ی بسیار دور ادامه دارد.
var farFuture = time.Date(9999, 1, 1, 0, 0, 0, 0, time.UTC)

const schema = `
CREATE TABLE IF NOT EXISTS employees (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	first_name TEXT NOT NULL,
	last_name TEXT NOT NULL DEFAULT '',
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS branches (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	name TEXT NOT NULL UNIQUE,
	active INTEGER NOT NULL DEFAULT 1,
	created_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS users (
	employee_id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL,
	role TEXT NOT NULL DEFAULT 'employee',
	FOREIGN KEY (employee_id) REFERENCES employees(id)
);

CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	employee_id INTEGER NOT NULL,
	expires_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS shifts (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	employee_id INTEGER NOT NULL,
	branch_id INTEGER NOT NULL DEFAULT 0,
	check_in TEXT NOT NULL,
	check_out TEXT,
	manual INTEGER NOT NULL DEFAULT 0,
	note TEXT NOT NULL DEFAULT '',
	created_at TEXT NOT NULL,
	edited_at TEXT
);

CREATE INDEX IF NOT EXISTS idx_shifts_employee ON shifts(employee_id);
CREATE INDEX IF NOT EXISTS idx_shifts_checkin ON shifts(check_in);
CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at);
`

// Store مسئول نگهداری و ذخیره‌سازی همه‌ی داده‌های برنامه در یک فایل SQLite است.
type Store struct {
	mu        sync.Mutex
	db        *sql.DB
	backupDir string
}

// New یک Store جدید می‌سازد؛ اگر فایل دیتابیس وجود نداشته باشد، آن را می‌سازد.
func New(dbPath, backupDir string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return nil, err
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("خطا در بازکردن دیتابیس: %w", err)
	}
	// یک اتصال کافی است (چند نفر همکار)؛ این کار خطاهای "database is locked" را حذف می‌کند.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec("PRAGMA journal_mode=WAL;"); err != nil {
		// اگر WAL روی این فایل‌سیستم پشتیبانی نشود، برنامه همچنان کار می‌کند.
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON;"); err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("خطا در ساخت جدول‌ها: %w", err)
	}

	return &Store{db: db, backupDir: backupDir}, nil
}

// ---------- کمک‌کننده‌های داخلی ----------

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rowScanner هم روی *sql.Row و هم روی *sql.Rows کار می‌کند.
type rowScanner interface {
	Scan(dest ...interface{}) error
}

func scanShift(row rowScanner) (models.Shift, error) {
	var sh models.Shift
	var checkIn string
	var checkOut sql.NullString
	var manual int
	var note string
	var createdAt string
	var editedAt sql.NullString

	if err := row.Scan(&sh.ID, &sh.EmployeeID, &sh.BranchID, &checkIn, &checkOut, &manual, &note, &createdAt, &editedAt); err != nil {
		return models.Shift{}, err
	}
	sh.Manual = manual != 0
	sh.Note = note
	if t, err := time.Parse(time.RFC3339, checkIn); err == nil {
		sh.CheckIn = t.Local()
	}
	if checkOut.Valid {
		if t, err := time.Parse(time.RFC3339, checkOut.String); err == nil {
			tt := t.Local()
			sh.CheckOut = &tt
		}
	}
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		sh.CreatedAt = t.Local()
	}
	if editedAt.Valid {
		if t, err := time.Parse(time.RFC3339, editedAt.String); err == nil {
			tt := t.Local()
			sh.EditedAt = &tt
		}
	}
	return sh, nil
}

const shiftCols = `id, employee_id, branch_id, check_in, check_out, manual, note, created_at, edited_at`

// intervalsOverlap بررسی می‌کند آیا دو بازه‌ی زمانی با هم تداخل دارند یا نه.
// یک بازه‌ی باز (checkOut == nil) به‌صورت "تا آینده‌ی دور" در نظر گرفته می‌شود.
func intervalsOverlap(aStart time.Time, aEnd *time.Time, bStart time.Time, bEnd *time.Time) bool {
	aEndT := farFuture
	if aEnd != nil {
		aEndT = *aEnd
	}
	bEndT := farFuture
	if bEnd != nil {
		bEndT = *bEnd
	}
	return aStart.Before(bEndT) && bStart.Before(aEndT)
}

// checkOverlapLocked بررسی می‌کند که بازه‌ی [checkIn, checkOut) برای employeeID
// با هیچ‌کدام از شیفت‌های موجود آن فرد (زنده یا دستی) تداخل نداشته باشد.
// excludeID برای ویرایش یک شیفت استفاده می‌شود تا خودش را با خودش مقایسه نکند.
func (s *Store) checkOverlapLocked(employeeID int, checkIn time.Time, checkOut *time.Time, excludeID int) error {
	rows, err := s.db.Query(`SELECT `+shiftCols+` FROM shifts WHERE employee_id = ?`, employeeID)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		existing, err := scanShift(rows)
		if err != nil {
			continue
		}
		if existing.ID == excludeID {
			continue
		}
		if intervalsOverlap(checkIn, checkOut, existing.CheckIn, existing.CheckOut) {
			kind := "زنده"
			if existing.Manual {
				kind = "دستی"
			}
			status := jalali.DateTimeString(existing.CheckIn)
			if existing.CheckOut != nil {
				status = status + " تا " + jalali.DateTimeString(*existing.CheckOut)
			} else {
				status = status + " (هنوز باز است)"
			}
			return fmt.Errorf("این بازه با یک شیفت %s دیگر همین فرد تداخل زمانی دارد: %s", kind, status)
		}
	}
	return nil
}

// backupLocked یک نسخه‌ی پشتیبان از دیتابیس می‌گیرد. باید فقط وقتی صدا زده شود که mu قفل است.
func (s *Store) backupLocked() {
	name := fmt.Sprintf("data-%s.db", time.Now().Format("20060102-150405"))
	path := filepath.Join(s.backupDir, name)
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", escaped)); err != nil {
		return
	}

	entries, err := os.ReadDir(s.backupDir)
	if err != nil {
		return
	}
	files := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	if len(files) > maxBackups {
		for _, f := range files[:len(files)-maxBackups] {
			_ = os.Remove(filepath.Join(s.backupDir, f))
		}
	}
}

// ---------- کارمندان ----------

func (s *Store) AddEmployee(first, last string) models.Employee {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO employees (first_name, last_name, active, created_at) VALUES (?, ?, 1, ?)`,
		first, last, now.Format(time.RFC3339))
	if err != nil {
		return models.Employee{}
	}
	id, _ := res.LastInsertId()
	s.backupLocked()
	return models.Employee{ID: int(id), FirstName: first, LastName: last, Active: true, CreatedAt: now}
}

func (s *Store) ListEmployees() []models.Employee {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, first_name, last_name, active, created_at FROM employees ORDER BY id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]models.Employee, 0)
	for rows.Next() {
		var e models.Employee
		var active int
		var createdAt string
		if err := rows.Scan(&e.ID, &e.FirstName, &e.LastName, &active, &createdAt); err != nil {
			continue
		}
		e.Active = active != 0
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			e.CreatedAt = t.Local()
		}
		out = append(out, e)
	}
	return out
}

func (s *Store) GetEmployee(id int) (models.Employee, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT id, first_name, last_name, active, created_at FROM employees WHERE id = ?`, id)
	var e models.Employee
	var active int
	var createdAt string
	if err := row.Scan(&e.ID, &e.FirstName, &e.LastName, &active, &createdAt); err != nil {
		return models.Employee{}, false
	}
	e.Active = active != 0
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		e.CreatedAt = t.Local()
	}
	return e, true
}

func (s *Store) SetEmployeeActive(id int, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE employees SET active = ? WHERE id = ?`, boolToInt(active), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("کارمند یافت نشد")
	}
	s.backupLocked()
	return nil
}

// ---------- شیفت‌ها ----------

// OpenShiftFor شیفت باز فعلی یک کارمند را برمی‌گرداند (اگر باشد).
func (s *Store) OpenShiftFor(employeeID int) (models.Shift, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT `+shiftCols+` FROM shifts WHERE employee_id = ? AND check_out IS NULL LIMIT 1`, employeeID)
	sh, err := scanShift(row)
	if err != nil {
		return models.Shift{}, false
	}
	return sh, true
}

func (s *Store) CheckIn(employeeID, branchID int) (models.Shift, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT `+shiftCols+` FROM shifts WHERE employee_id = ? AND check_out IS NULL LIMIT 1`, employeeID)
	if _, err := scanShift(row); err == nil {
		return models.Shift{}, fmt.Errorf("این فرد از قبل یک شیفت باز دارد؛ اول باید خروج بزند")
	}

	now := time.Now()
	// اگر برای همین لحظه یک شیفت دستی (که هنوز باز است یا محدوده‌اش شامل الان می‌شود) ثبت شده باشد،
	// اجازه نده یک شیفت زنده‌ی هم‌پوشان دیگر هم باز شود.
	if err := s.checkOverlapLocked(employeeID, now, nil, 0); err != nil {
		return models.Shift{}, err
	}

	res, err := s.db.Exec(`INSERT INTO shifts (employee_id, branch_id, check_in, check_out, manual, note, created_at) VALUES (?, ?, ?, NULL, 0, '', ?)`,
		employeeID, branchID, now.Format(time.RFC3339), now.Format(time.RFC3339))
	if err != nil {
		return models.Shift{}, err
	}
	id, _ := res.LastInsertId()
	s.backupLocked()
	return models.Shift{ID: int(id), EmployeeID: employeeID, BranchID: branchID, CheckIn: now, CreatedAt: now}, nil
}

func (s *Store) CheckOut(employeeID int) (models.Shift, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	row := s.db.QueryRow(`SELECT `+shiftCols+` FROM shifts WHERE employee_id = ? AND check_out IS NULL LIMIT 1`, employeeID)
	sh, err := scanShift(row)
	if err != nil {
		return models.Shift{}, fmt.Errorf("شیفت باز برای این فرد پیدا نشد")
	}

	now := time.Now()
	if now.Sub(sh.CheckIn) < minShiftDuration {
		return models.Shift{}, fmt.Errorf("چون کمتر از یک دقیقه از ورود گذشته، خروج ثبت نمی‌شود؛ لطفاً چند لحظه صبر کنید و دوباره امتحان کنید")
	}

	if _, err := s.db.Exec(`UPDATE shifts SET check_out = ? WHERE id = ?`, now.Format(time.RFC3339), sh.ID); err != nil {
		return models.Shift{}, err
	}
	sh.CheckOut = &now
	s.backupLocked()
	return sh, nil
}

// AddManualShift یک شیفت به‌صورت دستی (با تاخیر) ثبت می‌کند.
func (s *Store) AddManualShift(employeeID, branchID int, checkIn time.Time, checkOut *time.Time, note string) (models.Shift, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if checkOut != nil && checkOut.Before(checkIn) {
		return models.Shift{}, fmt.Errorf("زمان خروج نمی‌تواند قبل از زمان ورود باشد")
	}
	if checkOut != nil && checkOut.Sub(checkIn) < minShiftDuration {
		return models.Shift{}, fmt.Errorf("مدت شیفت نمی‌تواند کمتر از یک دقیقه باشد")
	}
	if err := s.checkOverlapLocked(employeeID, checkIn, checkOut, 0); err != nil {
		return models.Shift{}, err
	}

	now := time.Now()
	var coArg interface{}
	if checkOut != nil {
		coArg = checkOut.Format(time.RFC3339)
	}
	res, err := s.db.Exec(`INSERT INTO shifts (employee_id, branch_id, check_in, check_out, manual, note, created_at) VALUES (?, ?, ?, ?, 1, ?, ?)`,
		employeeID, branchID, checkIn.Format(time.RFC3339), coArg, note, now.Format(time.RFC3339))
	if err != nil {
		return models.Shift{}, err
	}
	id, _ := res.LastInsertId()
	s.backupLocked()
	return models.Shift{ID: int(id), EmployeeID: employeeID, BranchID: branchID, CheckIn: checkIn, CheckOut: checkOut, Manual: true, Note: note, CreatedAt: now}, nil
}

// UpdateShift یک شیفت موجود را ویرایش می‌کند (برای اصلاح خطاها).
func (s *Store) UpdateShift(id, branchID int, checkIn time.Time, checkOut *time.Time, note string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if checkOut != nil && checkOut.Before(checkIn) {
		return fmt.Errorf("زمان خروج نمی‌تواند قبل از زمان ورود باشد")
	}
	if checkOut != nil && checkOut.Sub(checkIn) < minShiftDuration {
		return fmt.Errorf("مدت شیفت نمی‌تواند کمتر از یک دقیقه باشد")
	}

	var employeeID int
	row := s.db.QueryRow(`SELECT employee_id FROM shifts WHERE id = ?`, id)
	if err := row.Scan(&employeeID); err != nil {
		return fmt.Errorf("شیفت یافت نشد")
	}

	if err := s.checkOverlapLocked(employeeID, checkIn, checkOut, id); err != nil {
		return err
	}

	var coArg interface{}
	if checkOut != nil {
		coArg = checkOut.Format(time.RFC3339)
	}
	now := time.Now()
	res, err := s.db.Exec(`UPDATE shifts SET branch_id = ?, check_in = ?, check_out = ?, note = ?, edited_at = ? WHERE id = ?`,
		branchID, checkIn.Format(time.RFC3339), coArg, note, now.Format(time.RFC3339), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("شیفت یافت نشد")
	}
	s.backupLocked()
	return nil
}

func (s *Store) DeleteShift(id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`DELETE FROM shifts WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("شیفت یافت نشد")
	}
	s.backupLocked()
	return nil
}

// ListShifts همه‌ی شیفت‌ها را برمی‌گرداند، به‌صورت اختیاری فیلتر شده بر اساس بازه زمانی.
// شیفتی در بازه در نظر گرفته می‌شود اگر بخشی از آن با بازه [from, to) همپوشانی داشته باشد.
func (s *Store) ListShifts(from, to time.Time, employeeID int) []models.Shift {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `SELECT ` + shiftCols + ` FROM shifts WHERE 1=1`
	args := make([]interface{}, 0, 1)
	if employeeID != 0 {
		query += ` AND employee_id = ?`
		args = append(args, employeeID)
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return []models.Shift{}
	}
	defer rows.Close()

	now := time.Now()
	out := make([]models.Shift, 0)
	for rows.Next() {
		sh, err := scanShift(rows)
		if err != nil {
			continue
		}
		end := now
		if sh.CheckOut != nil {
			end = *sh.CheckOut
		}
		if !from.IsZero() && end.Before(from) {
			continue
		}
		if !to.IsZero() && !sh.CheckIn.Before(to) {
			continue
		}
		out = append(out, sh)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CheckIn.Before(out[j].CheckIn) })
	return out
}

func (s *Store) OpenShifts() []models.Shift {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT ` + shiftCols + ` FROM shifts WHERE check_out IS NULL`)
	if err != nil {
		return []models.Shift{}
	}
	defer rows.Close()
	out := make([]models.Shift, 0)
	for rows.Next() {
		sh, err := scanShift(rows)
		if err != nil {
			continue
		}
		out = append(out, sh)
	}
	return out
}

func (s *Store) GetShift(id int) (models.Shift, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT `+shiftCols+` FROM shifts WHERE id = ?`, id)
	sh, err := scanShift(row)
	if err != nil {
		return models.Shift{}, false
	}
	return sh, true
}

// ---------- شعبه‌ها ----------

func (s *Store) AddBranch(name string) (models.Branch, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	res, err := s.db.Exec(`INSERT INTO branches (name, active, created_at) VALUES (?, 1, ?)`, name, now.Format(time.RFC3339))
	if err != nil {
		return models.Branch{}, fmt.Errorf("این نام شعبه قبلاً ثبت شده یا نامعتبر است")
	}
	id, _ := res.LastInsertId()
	s.backupLocked()
	return models.Branch{ID: int(id), Name: name, Active: true, CreatedAt: now}, nil
}

func (s *Store) ListBranches() []models.Branch {
	s.mu.Lock()
	defer s.mu.Unlock()
	rows, err := s.db.Query(`SELECT id, name, active, created_at FROM branches ORDER BY id ASC`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := make([]models.Branch, 0)
	for rows.Next() {
		var b models.Branch
		var active int
		var createdAt string
		if err := rows.Scan(&b.ID, &b.Name, &active, &createdAt); err != nil {
			continue
		}
		b.Active = active != 0
		if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
			b.CreatedAt = t.Local()
		}
		out = append(out, b)
	}
	return out
}

func (s *Store) GetBranch(id int) (models.Branch, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT id, name, active, created_at FROM branches WHERE id = ?`, id)
	var b models.Branch
	var active int
	var createdAt string
	if err := row.Scan(&b.ID, &b.Name, &active, &createdAt); err != nil {
		return models.Branch{}, false
	}
	b.Active = active != 0
	if t, err := time.Parse(time.RFC3339, createdAt); err == nil {
		b.CreatedAt = t.Local()
	}
	return b, true
}

func (s *Store) SetBranchActive(id int, active bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.Exec(`UPDATE branches SET active = ? WHERE id = ?`, boolToInt(active), id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("شعبه یافت نشد")
	}
	s.backupLocked()
	return nil
}

// ---------- کاربران (احراز هویت) ----------

// HasAdmin بررسی می‌کند آیا حداقل یک کاربر با نقش مدیر در سیستم وجود دارد.
func (s *Store) HasAdmin() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	var n int
	row := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = ?`, string(models.RoleAdmin))
	if err := row.Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// SetCredentials یک حساب کاربری برای یک کارمند می‌سازد یا آپدیت می‌کند.
func (s *Store) SetCredentials(employeeID int, username, passwordHash string, role models.Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`
		INSERT INTO users (employee_id, username, password_hash, role) VALUES (?, ?, ?, ?)
		ON CONFLICT(employee_id) DO UPDATE SET username = excluded.username, password_hash = excluded.password_hash, role = excluded.role
	`, employeeID, username, passwordHash, string(role))
	if err != nil {
		return fmt.Errorf("این نام کاربری قبلاً استفاده شده است")
	}
	s.backupLocked()
	return nil
}

func (s *Store) GetUserByUsername(username string) (models.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT employee_id, username, password_hash, role FROM users WHERE username = ?`, username)
	var u models.User
	var role string
	if err := row.Scan(&u.EmployeeID, &u.Username, &u.PasswordHash, &role); err != nil {
		return models.User{}, false
	}
	u.Role = models.Role(role)
	return u, true
}

func (s *Store) GetUserByEmployee(employeeID int) (models.User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT employee_id, username, password_hash, role FROM users WHERE employee_id = ?`, employeeID)
	var u models.User
	var role string
	if err := row.Scan(&u.EmployeeID, &u.Username, &u.PasswordHash, &role); err != nil {
		return models.User{}, false
	}
	u.Role = models.Role(role)
	return u, true
}

// ---------- نشست‌ها (Sessions) ----------

func (s *Store) CreateSession(employeeID int, token string, ttl time.Duration) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	expires := time.Now().Add(ttl)
	_, err := s.db.Exec(`INSERT INTO sessions (token, employee_id, expires_at) VALUES (?, ?, ?)`,
		token, employeeID, expires.Format(time.RFC3339))
	return err
}

func (s *Store) GetSession(token string) (models.Session, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	row := s.db.QueryRow(`SELECT token, employee_id, expires_at FROM sessions WHERE token = ?`, token)
	var sess models.Session
	var expiresAt string
	if err := row.Scan(&sess.Token, &sess.EmployeeID, &expiresAt); err != nil {
		return models.Session{}, false
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return models.Session{}, false
	}
	sess.ExpiresAt = t.Local()
	if sess.ExpiresAt.Before(time.Now()) {
		return models.Session{}, false
	}
	return sess, true
}

func (s *Store) DeleteSession(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// ---------- کارمندان + حساب (برای نمایش در پنل مدیر) ----------

// ListEmployeesWithAccounts کارمندان را همراه با username/role (اگر حساب داشته باشند) برمی‌گرداند.
func (s *Store) ListEmployeesWithAccounts() []models.Employee {
	employees := s.ListEmployees()
	s.mu.Lock()
	rows, err := s.db.Query(`SELECT employee_id, username, role FROM users`)
	accounts := map[int][2]string{}
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var empID int
			var username, role string
			if rows.Scan(&empID, &username, &role) == nil {
				accounts[empID] = [2]string{username, role}
			}
		}
	}
	s.mu.Unlock()

	out := make([]models.Employee, 0, len(employees))
	for _, e := range employees {
		if acc, ok := accounts[e.ID]; ok {
			e.Username = acc[0]
			e.Role = models.Role(acc[1])
			e.HasAccount = true
		}
		out = append(out, e)
	}
	return out
}

// ---------- آنلاین‌های الان (برای پنل مدیر) ----------

type OnlineNowEntry struct {
	Shift        models.Shift
	EmployeeName string
	BranchName   string
}

// OnlineNow لیست شیفت‌های بازِ فعلی را به همراه نام کارمند و شعبه برمی‌گرداند.
func (s *Store) OnlineNow() []OnlineNowEntry {
	open := s.OpenShifts()
	out := make([]OnlineNowEntry, 0, len(open))
	for _, sh := range open {
		name := "-"
		if emp, ok := s.GetEmployee(sh.EmployeeID); ok {
			name = emp.FullName()
		}
		branchName := "-"
		if sh.BranchID != 0 {
			if b, ok := s.GetBranch(sh.BranchID); ok {
				branchName = b.Name
			}
		}
		out = append(out, OnlineNowEntry{Shift: sh, EmployeeName: name, BranchName: branchName})
	}
	return out
}
