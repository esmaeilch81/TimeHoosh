package jalali

import (
	"fmt"
	"time"

	jalaali "github.com/jalaali/go-jalaali"
)

var monthNames = []string{
	"فروردین", "اردیبهشت", "خرداد", "تیر", "مرداد", "شهریور",
	"مهر", "آبان", "آذر", "دی", "بهمن", "اسفند",
}

var weekdayNames = map[time.Weekday]string{
	time.Saturday:  "شنبه",
	time.Sunday:    "یکشنبه",
	time.Monday:    "دوشنبه",
	time.Tuesday:   "سه‌شنبه",
	time.Wednesday: "چهارشنبه",
	time.Thursday:  "پنجشنبه",
	time.Friday:    "جمعه",
}

// ToPersianDigits اعداد لاتین را به رقم فارسی تبدیل می‌کند.
func ToPersianDigits(s string) string {
	fa := []rune("۰۱۲۳۴۵۶۷۸۹")
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r >= '0' && r <= '9' {
			out = append(out, fa[r-'0'])
		} else {
			out = append(out, r)
		}
	}
	return string(out)
}

// YMD سال، ماه و روز شمسی یک زمان را برمی‌گرداند.
func YMD(t time.Time) (int, int, int) {
	jy, jm, jd, err := jalaali.ToJalaali(t.Year(), t.Month(), t.Day())
	if err != nil {
		return 0, 0, 0
	}
	return jy, int(jm), jd
}

// DateString تاریخ شمسی به‌صورت ۱۴۰۵/۰۴/۱۵
func DateString(t time.Time) string {
	jy, jm, jd := YMD(t)
	s := fmt.Sprintf("%04d/%02d/%02d", jy, jm, jd)
	return ToPersianDigits(s)
}

// DateStringLong تاریخ شمسی به‌صورت شنبه ۱۵ تیر ۱۴۰۵
func DateStringLong(t time.Time) string {
	jy, jm, jd := YMD(t)
	wd := weekdayNames[t.Weekday()]
	s := fmt.Sprintf("%s %d %s %d", wd, jd, monthNames[jm-1], jy)
	return ToPersianDigits(s)
}

// TimeString ساعت به‌صورت ۱۴:۰۵
func TimeString(t time.Time) string {
	return ToPersianDigits(t.Format("15:04"))
}

// DateTimeString تاریخ و ساعت با هم
func DateTimeString(t time.Time) string {
	return DateString(t) + " - " + TimeString(t)
}

// FromYMD یک تاریخ شمسی (و ساعت اختیاری) را به time.Time گرگوری تبدیل می‌کند.
func FromYMD(jy, jm, jd, hour, minute int) (time.Time, error) {
	gy, gm, gd, err := jalaali.ToGregorian(jy, jalaali.Month(jm), jd)
	if err != nil {
		return time.Time{}, err
	}
	return time.Date(gy, gm, gd, hour, minute, 0, 0, time.Local), nil
}

// MonthRange بازه‌ی گرگوری متناظر با یک ماه شمسی را برمی‌گرداند [start, end)
func MonthRange(jy, jm int) (time.Time, time.Time, error) {
	start, err := FromYMD(jy, jm, 1, 0, 0)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	nextJY, nextJM := jy, jm+1
	if nextJM > 12 {
		nextJM = 1
		nextJY++
	}
	end, err := FromYMD(nextJY, nextJM, 1, 0, 0)
	if err != nil {
		return time.Time{}, time.Time{}, err
	}
	return start, end, nil
}

// CurrentYM سال و ماه شمسی امروز
func CurrentYM() (int, int) {
	jy, jm, _ := YMD(time.Now())
	return jy, jm
}

// MonthName نام ماه شمسی (۱ تا ۱۲)
func MonthName(jm int) string {
	if jm < 1 || jm > 12 {
		return ""
	}
	return monthNames[jm-1]
}
