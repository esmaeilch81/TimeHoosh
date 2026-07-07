package main

import (
	"crypto/rand"
	"embed"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"

	"shifttracker/internal/excelexport"
	"shifttracker/internal/jalali"
	"shifttracker/internal/models"
	"shifttracker/internal/store"
)

//go:embed web/static
var staticFS embed.FS

var db *store.Store

const sessionCookieName = "session_token"
const sessionTTL = 12 * time.Hour

func main() {
	exeDir, _ := os.Getwd()
	dataPath := filepath.Join(exeDir, "data", "data.db")
	backupDir := filepath.Join(exeDir, "data", "backups")

	// ---------- پرچم‌های خط فرمان برای راه‌اندازی اولیه (bootstrap) ----------
	bootstrapUser := flag.String("bootstrap-admin-username", "", "نام کاربری مدیر اولیه (فقط برای راه‌اندازی اول سیستم)")
	bootstrapPass := flag.String("bootstrap-admin-password", "", "رمز عبور مدیر اولیه")
	bootstrapFirst := flag.String("bootstrap-admin-firstname", "مدیر", "نام مدیر اولیه")
	bootstrapLast := flag.String("bootstrap-admin-lastname", "", "نام خانوادگی مدیر اولیه")
	flag.Parse()

	var err error
	db, err = store.New(dataPath, backupDir)
	if err != nil {
		log.Fatalf("خطا در راه‌اندازی دیتابیس: %v", err)
	}

	if *bootstrapUser != "" || *bootstrapPass != "" {
		runBootstrap(*bootstrapUser, *bootstrapPass, *bootstrapFirst, *bootstrapLast)
		return
	}

	sub, err := fs.Sub(staticFS, "web/static")
	if err != nil {
		log.Fatal(err)
	}
	http.Handle("/", http.FileServer(http.FS(sub)))

	// احراز هویت
	http.HandleFunc("/api/auth/login", handleLogin)
	http.HandleFunc("/api/auth/logout", handleLogout)
	http.HandleFunc("/api/auth/me", handleMe)

	// پرسنل و حساب‌ها (فقط مدیر برای ساخت/ویرایش)
	http.HandleFunc("/api/employees", requireAuth(handleEmployees))
	http.HandleFunc("/api/employees/toggle", requireAdmin(handleToggleEmployee))
	http.HandleFunc("/api/employees/credentials", requireAdmin(handleSetCredentials))

	// شعبه‌ها
	http.HandleFunc("/api/branches", requireAuth(handleBranches))
	http.HandleFunc("/api/branches/toggle", requireAdmin(handleToggleBranch))

	// ورود/خروج زنده
	http.HandleFunc("/api/checkin", requireAuth(handleCheckIn))
	http.HandleFunc("/api/checkout", requireAuth(handleCheckOut))

	// شیفت‌ها
	http.HandleFunc("/api/shifts", requireAuth(handleShifts))
	http.HandleFunc("/api/shifts/manual", requireAdmin(handleManualShift))
	http.HandleFunc("/api/shifts/update", requireAdmin(handleUpdateShift))
	http.HandleFunc("/api/shifts/delete", requireAdmin(handleDeleteShift))

	// داشبورد (فقط مدیر: آنلاین‌های الان و خلاصه‌ی همه)
	http.HandleFunc("/api/dashboard", requireAdmin(handleDashboard))
	http.HandleFunc("/api/export", requireAdmin(handleExport))

	port := "8080"
	fmt.Println("======================================")
	fmt.Println(" سامانه ثبت ساعت کاری پرسنل")
	fmt.Printf(" آدرس: http://localhost:%s\n", port)
	if !db.HasAdmin() {
		fmt.Println(" ⚠ هنوز هیچ مدیری در سیستم ثبت نشده است.")
		fmt.Println("   برای ساخت مدیر اولیه، برنامه را متوقف کنید و با این پرچم‌ها اجرا کنید:")
		fmt.Println("   ./shifttracker -bootstrap-admin-username=admin -bootstrap-admin-password=YOUR_PASSWORD")
	}
	fmt.Println(" برای خروج: Ctrl+C")
	fmt.Println("======================================")
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// runBootstrap یک کارمند مدیر می‌سازد (اگر لازم باشد) و حساب کاربری مدیر را روی آن تنظیم می‌کند.
// این تنها راه ساخت اولین مدیر است و از طریق HTTP در دسترس نیست، تا کسی از بیرون نتواند
// بدون دسترسی به خود سرور یک حساب مدیر برای خودش بسازد.
func runBootstrap(username, password, first, last string) {
	if username == "" || password == "" {
		log.Fatal("برای bootstrap باید هم -bootstrap-admin-username و هم -bootstrap-admin-password را بدهید")
	}
	if len(password) < 6 {
		log.Fatal("رمز عبور باید حداقل ۶ کاراکتر باشد")
	}

	if existing, ok := db.GetUserByUsername(username); ok {
		hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("خطا در ساخت رمز: %v", err)
		}
		if err := db.SetCredentials(existing.EmployeeID, username, string(hash), models.RoleAdmin); err != nil {
			log.Fatalf("خطا در آپدیت مدیر: %v", err)
		}
		fmt.Printf("رمز عبور مدیر «%s» به‌روزرسانی شد.\n", username)
		return
	}

	emp := db.AddEmployee(first, last)
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Fatalf("خطا در ساخت رمز: %v", err)
	}
	if err := db.SetCredentials(emp.ID, username, string(hash), models.RoleAdmin); err != nil {
		log.Fatalf("خطا در ساخت حساب مدیر: %v", err)
	}
	fmt.Printf("مدیر اولیه ساخته شد. نام کاربری: %s\n", username)
}

// ---------- کمک‌کننده‌ها ----------

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

type shiftView struct {
	ID           int     `json:"id"`
	EmployeeID   int     `json:"employee_id"`
	EmployeeName string  `json:"employee_name"`
	BranchID     int     `json:"branch_id"`
	BranchName   string  `json:"branch_name"`
	CheckInISO   string  `json:"check_in_iso"`
	CheckInJ     string  `json:"check_in_jalali"`
	CheckOutISO  string  `json:"check_out_iso"`
	CheckOutJ    string  `json:"check_out_jalali"`
	Open         bool    `json:"open"`
	Manual       bool    `json:"manual"`
	Note         string  `json:"note"`
	Hours        float64 `json:"hours"`
}

func toShiftView(sh models.Shift, employees map[int]models.Employee, branches map[int]models.Branch) shiftView {
	emp := employees[sh.EmployeeID]
	branchName := ""
	if b, ok := branches[sh.BranchID]; ok {
		branchName = b.Name
	}
	sv := shiftView{
		ID:           sh.ID,
		EmployeeID:   sh.EmployeeID,
		EmployeeName: emp.FullName(),
		BranchID:     sh.BranchID,
		BranchName:   branchName,
		CheckInISO:   sh.CheckIn.Format(time.RFC3339),
		CheckInJ:     jalali.DateTimeString(sh.CheckIn),
		Open:         sh.IsOpen(),
		Manual:       sh.Manual,
		Note:         sh.Note,
		Hours:        sh.Duration(time.Now()).Hours(),
	}
	if sh.CheckOut != nil {
		sv.CheckOutISO = sh.CheckOut.Format(time.RFC3339)
		sv.CheckOutJ = jalali.DateTimeString(*sh.CheckOut)
	}
	return sv
}

func employeeMap() map[int]models.Employee {
	m := map[int]models.Employee{}
	for _, e := range db.ListEmployees() {
		m[e.ID] = e
	}
	return m
}

func branchMap() map[int]models.Branch {
	m := map[int]models.Branch{}
	for _, b := range db.ListBranches() {
		m[b.ID] = b
	}
	return m
}

// ---------- احراز هویت و session ----------

type authedHandler func(w http.ResponseWriter, r *http.Request, user models.User)

func newSessionToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d-%d", time.Now().UnixNano(), os.Getpid())
	}
	return hex.EncodeToString(b)
}

func currentUser(r *http.Request) (models.User, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return models.User{}, false
	}
	sess, ok := db.GetSession(cookie.Value)
	if !ok {
		return models.User{}, false
	}
	user, ok := db.GetUserByEmployee(sess.EmployeeID)
	if !ok {
		return models.User{}, false
	}
	// بررسی کنید که کارمند فعال است
	emp, ok := db.GetEmployee(user.EmployeeID)
	if !ok || !emp.Active {
		return models.User{}, false
	}
	return user, true
}

// requireAuth اجازه می‌دهد هر کاربر لاگین‌شده (مدیر یا کارمند) درخواست را انجام دهد.
func requireAuth(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok {
			writeError(w, 401, "لطفاً ابتدا وارد شوید")
			return
		}
		next(w, r, user)
	}
}

// requireAdmin فقط به مدیر اجازه‌ی دسترسی می‌دهد.
func requireAdmin(next authedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, ok := currentUser(r)
		if !ok {
			writeError(w, 401, "لطفاً ابتدا وارد شوید")
			return
		}
		if user.Role != models.RoleAdmin {
			writeError(w, 403, "شما دسترسی مدیر ندارید")
			return
		}
		next(w, r, user)
	}
}

func handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	user, ok := db.GetUserByUsername(body.Username)
	if !ok {
		writeError(w, 401, "نام کاربری یا رمز عبور نادرست است")
		return
	}
	// بررسی کنید که کارمند فعال است
	emp, ok := db.GetEmployee(user.EmployeeID)
	if !ok || !emp.Active {
		writeError(w, 401, "این کارمند غیرفعال شده است")
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(body.Password)) != nil {
		writeError(w, 401, "نام کاربری یا رمز عبور نادرست است")
		return
	}

	token := newSessionToken()
	if err := db.CreateSession(user.EmployeeID, token, sessionTTL); err != nil {
		writeError(w, 500, "خطا در ساخت نشست ورود")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Expires:  time.Now().Add(sessionTTL),
	})

	writeJSON(w, 200, map[string]interface{}{
		"employee_id": user.EmployeeID,
		"username":    user.Username,
		"role":        user.Role,
		"full_name":   emp.FullName(),
	})
}

func handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		_ = db.DeleteSession(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		MaxAge:   -1,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := currentUser(r)
	if !ok {
		writeError(w, 401, "وارد نشده‌اید")
		return
	}
	emp, _ := db.GetEmployee(user.EmployeeID)
	writeJSON(w, 200, map[string]interface{}{
		"employee_id": user.EmployeeID,
		"username":    user.Username,
		"role":        user.Role,
		"full_name":   emp.FullName(),
	})
}

// handleSetCredentials حساب کاربری (username/password/role) یک کارمند را می‌سازد یا آپدیت می‌کند.
// فقط مدیر می‌تواند این کار را انجام دهد (bootstrap اولین مدیر از راه CLI انجام می‌شود، نه اینجا).
func handleSetCredentials(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		EmployeeID int    `json:"employee_id"`
		Username   string `json:"username"`
		Password   string `json:"password"`
		Role       string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if body.Username == "" || body.Password == "" {
		writeError(w, 400, "نام کاربری و رمز عبور الزامی است")
		return
	}
	if len(body.Password) < 6 {
		writeError(w, 400, "رمز عبور باید حداقل ۶ کاراکتر باشد")
		return
	}
	role := models.RoleEmployee
	if body.Role == string(models.RoleAdmin) {
		role = models.RoleAdmin
	}
	if _, ok := db.GetEmployee(body.EmployeeID); !ok {
		writeError(w, 400, "کارمند یافت نشد")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, 500, "خطا در ساخت رمز")
		return
	}
	if err := db.SetCredentials(body.EmployeeID, body.Username, string(hash), role); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- پرسنل ----------

func handleEmployees(w http.ResponseWriter, r *http.Request, user models.User) {
	switch r.Method {
	case http.MethodGet:
		if user.Role == models.RoleAdmin {
			writeJSON(w, 200, db.ListEmployeesWithAccounts())
		} else {
			writeJSON(w, 200, db.ListEmployees())
		}
	case http.MethodPost:
		if user.Role != models.RoleAdmin {
			writeError(w, 403, "شما دسترسی مدیر ندارید")
			return
		}
		var body struct {
			FirstName string `json:"first_name"`
			LastName  string `json:"last_name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "داده ورودی نامعتبر است")
			return
		}
		if body.FirstName == "" {
			writeError(w, 400, "نام الزامی است")
			return
		}
		e := db.AddEmployee(body.FirstName, body.LastName)
		writeJSON(w, 200, e)
	default:
		writeError(w, 405, "متد غیرمجاز")
	}
}

func handleToggleEmployee(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		ID     int  `json:"id"`
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if err := db.SetEmployeeActive(body.ID, body.Active); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- شعبه‌ها ----------

func handleBranches(w http.ResponseWriter, r *http.Request, user models.User) {
	switch r.Method {
	case http.MethodGet:
		writeJSON(w, 200, db.ListBranches())
	case http.MethodPost:
		if user.Role != models.RoleAdmin {
			writeError(w, 403, "شما دسترسی مدیر ندارید")
			return
		}
		var body struct {
			Name string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeError(w, 400, "داده ورودی نامعتبر است")
			return
		}
		if body.Name == "" {
			writeError(w, 400, "نام شعبه الزامی است")
			return
		}
		b, err := db.AddBranch(body.Name)
		if err != nil {
			writeError(w, 400, err.Error())
			return
		}
		writeJSON(w, 200, b)
	default:
		writeError(w, 405, "متد غیرمجاز")
	}
}

func handleToggleBranch(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		ID     int  `json:"id"`
		Active bool `json:"active"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if err := db.SetBranchActive(body.ID, body.Active); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- ورود و خروج زنده ----------

// resolveOwnEmployeeID اگر کاربر مدیر است و employee_id در بدنه فرستاده شده، همان را برمی‌گرداند
// (چون مدیر می‌تواند برای دیگران هم ثبت دستی انجام دهد)؛ برای کارمند عادی همیشه فقط خودش.
func resolveOwnEmployeeID(user models.User, requested int) (int, error) {
	if user.Role == models.RoleAdmin {
		if requested != 0 {
			return requested, nil
		}
		return user.EmployeeID, nil
	}
	if requested != 0 && requested != user.EmployeeID {
		return 0, fmt.Errorf("شما فقط می‌توانید برای خودتان ثبت کنید")
	}
	return user.EmployeeID, nil
}

func handleCheckIn(w http.ResponseWriter, r *http.Request, user models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		EmployeeID int `json:"employee_id"`
		BranchID   int `json:"branch_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	empID, err := resolveOwnEmployeeID(user, body.EmployeeID)
	if err != nil {
		writeError(w, 403, err.Error())
		return
	}
	if body.BranchID == 0 {
		writeError(w, 400, "انتخاب شعبه الزامی است")
		return
	}
	sh, err := db.CheckIn(empID, body.BranchID)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, toShiftView(sh, employeeMap(), branchMap()))
}

func handleCheckOut(w http.ResponseWriter, r *http.Request, user models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		EmployeeID int `json:"employee_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	empID, err := resolveOwnEmployeeID(user, body.EmployeeID)
	if err != nil {
		writeError(w, 403, err.Error())
		return
	}
	sh, err := db.CheckOut(empID)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, toShiftView(sh, employeeMap(), branchMap()))
}

// ---------- ثبت دستی و ویرایش (فقط مدیر) ----------

type manualBody struct {
	EmployeeID int    `json:"employee_id"`
	BranchID   int    `json:"branch_id"`
	InYear     int    `json:"in_year"`
	InMonth    int    `json:"in_month"`
	InDay      int    `json:"in_day"`
	InHour     int    `json:"in_hour"`
	InMinute   int    `json:"in_minute"`
	HasOut     bool   `json:"has_out"`
	OutYear    int    `json:"out_year"`
	OutMonth   int    `json:"out_month"`
	OutDay     int    `json:"out_day"`
	OutHour    int    `json:"out_hour"`
	OutMinute  int    `json:"out_minute"`
	Note       string `json:"note"`
}

func (b manualBody) parseTimes() (time.Time, *time.Time, error) {
	checkIn, err := jalali.FromYMD(b.InYear, b.InMonth, b.InDay, b.InHour, b.InMinute)
	if err != nil {
		return time.Time{}, nil, fmt.Errorf("تاریخ ورود نامعتبر است")
	}
	var checkOut *time.Time
	if b.HasOut {
		co, err := jalali.FromYMD(b.OutYear, b.OutMonth, b.OutDay, b.OutHour, b.OutMinute)
		if err != nil {
			return time.Time{}, nil, fmt.Errorf("تاریخ خروج نامعتبر است")
		}
		checkOut = &co
	}
	return checkIn, checkOut, nil
}

func handleManualShift(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var b manualBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if b.BranchID == 0 {
		writeError(w, 400, "انتخاب شعبه الزامی است")
		return
	}
	checkIn, checkOut, err := b.parseTimes()
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	sh, err := db.AddManualShift(b.EmployeeID, b.BranchID, checkIn, checkOut, b.Note)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, toShiftView(sh, employeeMap(), branchMap()))
}

func handleUpdateShift(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		ID int `json:"id"`
		manualBody
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if body.BranchID == 0 {
		writeError(w, 400, "انتخاب شعبه الزامی است")
		return
	}
	checkIn, checkOut, err := body.manualBody.parseTimes()
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := db.UpdateShift(body.ID, body.BranchID, checkIn, checkOut, body.Note); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func handleDeleteShift(w http.ResponseWriter, r *http.Request, _ models.User) {
	if r.Method != http.MethodPost {
		writeError(w, 405, "متد غیرمجاز")
		return
	}
	var body struct {
		ID int `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, 400, "داده ورودی نامعتبر است")
		return
	}
	if err := db.DeleteShift(body.ID); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- شیفت‌ها و داشبورد ----------

// handleShifts: کارمند عادی فقط شیفت‌های خودش را می‌بیند (حتی اگر employee_id دیگری بفرستد،
// نادیده گرفته می‌شود)؛ مدیر می‌تواند با employee_id=0 همه را ببیند یا فیلتر کند.
func handleShifts(w http.ResponseWriter, r *http.Request, user models.User) {
	q := r.URL.Query()
	empID, _ := strconv.Atoi(q.Get("employee_id"))
	if user.Role != models.RoleAdmin {
		empID = user.EmployeeID
	}
	from, to, err := parseRange(q)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	shifts := db.ListShifts(from, to, empID)
	sort.Slice(shifts, func(i, j int) bool { return shifts[i].CheckIn.After(shifts[j].CheckIn) })

	total := len(shifts)
	page, _ := strconv.Atoi(q.Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(q.Get("page_size"))
	if pageSize <= 0 {
		pageSize = total
		if pageSize <= 0 {
			pageSize = 1
		}
	}

	totalPages := (total + pageSize - 1) / pageSize
	if totalPages < 1 {
		totalPages = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pageSize
	if start > total {
		start = total
	}
	if start < 0 {
		start = 0
	}
	end := start + pageSize
	if end > total {
		end = total
	}

	pageShifts := shifts[start:end]
	employees := employeeMap()
	branches := branchMap()
	views := make([]shiftView, 0, len(pageShifts))
	for _, sh := range pageShifts {
		views = append(views, toShiftView(sh, employees, branches))
	}
	writeJSON(w, 200, map[string]interface{}{
		"items":       views,
		"total":       total,
		"page":        page,
		"page_size":   pageSize,
		"total_pages": totalPages,
	})
}

// handleDashboard فقط برای مدیر است: خلاصه‌ی همه‌ی پرسنل و لیست «کی الان آنلاین است، در کدام شعبه».
func handleDashboard(w http.ResponseWriter, r *http.Request, _ models.User) {
	employees := employeeMap()
	branches := branchMap()

	online := db.OnlineNow()
	onlineViews := make([]shiftView, 0, len(online))
	for _, entry := range online {
		onlineViews = append(onlineViews, toShiftView(entry.Shift, employees, branches))
	}

	jy, jm, jd := jalali.YMD(time.Now())
	from, to, _ := jalali.MonthRange(jy, jm)
	monthShifts := db.ListShifts(from, to, 0)
	totals := map[int]float64{}
	for _, sh := range monthShifts {
		totals[sh.EmployeeID] += sh.Duration(time.Now()).Hours()
	}
	type empSummary struct {
		Employee models.Employee `json:"employee"`
		Hours    float64         `json:"hours"`
	}
	summaries := make([]empSummary, 0)
	for _, e := range db.ListEmployees() {
		summaries = append(summaries, empSummary{Employee: e, Hours: totals[e.ID]})
	}

	writeJSON(w, 200, map[string]interface{}{
		"open_shifts":      onlineViews,
		"month_summary":    summaries,
		"jalali_month":     fmt.Sprintf("%s %d", jalali.MonthName(jm), jy),
		"today":            jalali.DateStringLong(time.Now()),
		"jalali_year":      jy,
		"jalali_month_num": jm,
		"jalali_day":       jd,
	})
}

func parseRange(q map[string][]string) (time.Time, time.Time, error) {
	get := func(k string) string {
		if v, ok := q[k]; ok && len(v) > 0 {
			return v[0]
		}
		return ""
	}
	rangeType := get("range")
	now := time.Now()
	jy, jm := jalali.CurrentYM()

	switch rangeType {
	case "", "this_month":
		return jalali.MonthRange(jy, jm)
	case "last_month":
		lm, ly := jm-1, jy
		if lm < 1 {
			lm = 12
			ly--
		}
		return jalali.MonthRange(ly, lm)
	case "custom":
		fy, _ := strconv.Atoi(get("from_year"))
		fm, _ := strconv.Atoi(get("from_month"))
		fd, _ := strconv.Atoi(get("from_day"))
		ty, _ := strconv.Atoi(get("to_year"))
		tm, _ := strconv.Atoi(get("to_month"))
		td, _ := strconv.Atoi(get("to_day"))
		from, err := jalali.FromYMD(fy, fm, fd, 0, 0)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("تاریخ شروع نامعتبر است")
		}
		to, err := jalali.FromYMD(ty, tm, td, 23, 59)
		if err != nil {
			return time.Time{}, time.Time{}, fmt.Errorf("تاریخ پایان نامعتبر است")
		}
		to = to.Add(time.Minute)
		return from, to, nil
	case "all":
		return time.Time{}, time.Time{}, nil
	default:
		_ = now
		return jalali.MonthRange(jy, jm)
	}
}

func handleExport(w http.ResponseWriter, r *http.Request, _ models.User) {
	q := r.URL.Query()
	qm := map[string][]string{}
	for k, v := range q {
		qm[k] = v
	}
	from, to, err := parseRange(qm)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	empID, _ := strconv.Atoi(q.Get("employee_id"))
	shifts := db.ListShifts(from, to, empID)
	employees := employeeMap()

	fileBytes, err := excelexport.Build(shifts, employees, "گزارش ساعت کاری")
	if err != nil {
		writeError(w, 500, "خطا در ساخت فایل اکسل: "+err.Error())
		return
	}
	fname := fmt.Sprintf("گزارش-ساعت-کاری-%s.xlsx", time.Now().Format("2006-01-02"))
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", "attachment; filename=\""+fname+"\"")
	w.Write(fileBytes)
}
