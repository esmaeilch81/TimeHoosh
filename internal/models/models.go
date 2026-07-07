package models

import "time"

// Role نقش یک کاربر در سیستم است.
type Role string

const (
	RoleAdmin    Role = "admin"
	RoleEmployee Role = "employee"
)

// Branch یک شعبه است.
type Branch struct {
	ID        int       `json:"id"`
	Name      string    `json:"name"`
	Active    bool      `json:"active"`
	CreatedAt time.Time `json:"created_at"`
}

// Employee یک نفر از پرسنل است.
// یک Employee می‌تواند حساب کاربری (User) داشته باشد یا نداشته باشد؛
// اگر حساب داشته باشد با username/password وارد سیستم می‌شود.
type Employee struct {
	ID         int       `json:"id"`
	FirstName  string    `json:"first_name"`
	LastName   string    `json:"last_name"`
	Active     bool      `json:"active"`
	CreatedAt  time.Time `json:"created_at"`
	Username   string    `json:"username,omitempty"`
	Role       Role      `json:"role,omitempty"`
	HasAccount bool      `json:"has_account"`
}

func (e Employee) FullName() string {
	return e.FirstName + " " + e.LastName
}

// User اطلاعات احراز هویت یک کارمند است (جدا از پروفایل او).
type User struct {
	EmployeeID   int
	Username     string
	PasswordHash string
	Role         Role
}

// Session یک نشست فعال ورود است.
type Session struct {
	Token      string
	EmployeeID int
	ExpiresAt  time.Time
}

// Shift یک بازه‌ی ورود تا خروج برای یک کارمند است.
// CheckOut می‌تواند nil باشد یعنی شیفت هنوز باز است.
type Shift struct {
	ID         int        `json:"id"`
	EmployeeID int        `json:"employee_id"`
	BranchID   int        `json:"branch_id"` // شعبه‌ای که این شیفت در آن ثبت شده (۰ یعنی نامشخص)
	CheckIn    time.Time  `json:"check_in"`
	CheckOut   *time.Time `json:"check_out"`
	Manual     bool       `json:"manual"`  // true اگر به‌صورت دستی/با تاخیر ثبت شده
	Note       string     `json:"note"`    // توضیح برای ثبت دستی
	CreatedAt  time.Time  `json:"created_at"`
	EditedAt   *time.Time `json:"edited_at,omitempty"`
}

// IsOpen یعنی شیفت هنوز بسته نشده (فرد هنوز خروج نزده).
func (s Shift) IsOpen() bool {
	return s.CheckOut == nil
}

// Duration مدت شیفت را برمی‌گرداند. اگر شیفت باز باشد نسبت به now حساب می‌شود.
func (s Shift) Duration(now time.Time) time.Duration {
	end := now
	if s.CheckOut != nil {
		end = *s.CheckOut
	}
	if end.Before(s.CheckIn) {
		return 0
	}
	return end.Sub(s.CheckIn)
}

