package excelexport

import (
	"fmt"
	"sort"
	"time"

	"github.com/xuri/excelize/v2"

	"shifttracker/internal/jalali"
	"shifttracker/internal/models"
)

// Build یک فایل اکسل کامل از روی شیفت‌های داده شده می‌سازد و byte آن را برمی‌گرداند.
// employees برای نگاشت شناسه به نام لازم است.
func Build(shifts []models.Shift, employees map[int]models.Employee, title string) ([]byte, error) {
	f := excelize.NewFile()
	defer f.Close()

	detailSheet := "جزئیات"
	summarySheet := "خلاصه پرسنل"
	f.SetSheetName("Sheet1", detailSheet)
	f.NewSheet(summarySheet)

	if err := f.SetSheetView(detailSheet, 0, &excelize.ViewOptions{RightToLeft: boolPtr(true)}); err != nil {
		return nil, err
	}
	if err := f.SetSheetView(summarySheet, 0, &excelize.ViewOptions{RightToLeft: boolPtr(true)}); err != nil {
		return nil, err
	}

	headerStyle, err := f.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Bold: true, Color: "FFFFFF", Size: 12},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"2F5597"}, Pattern: 1},
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "000000", Style: 1},
			{Type: "right", Color: "000000", Style: 1},
			{Type: "top", Color: "000000", Style: 1},
			{Type: "bottom", Color: "000000", Style: 1},
		},
	})
	if err != nil {
		return nil, err
	}
	cellStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Border: []excelize.Border{
			{Type: "left", Color: "D9D9D9", Style: 1},
			{Type: "right", Color: "D9D9D9", Style: 1},
			{Type: "top", Color: "D9D9D9", Style: 1},
			{Type: "bottom", Color: "D9D9D9", Style: 1},
		},
	})
	manualStyle, _ := f.NewStyle(&excelize.Style{
		Alignment: &excelize.Alignment{Horizontal: "center", Vertical: "center"},
		Fill:      excelize.Fill{Type: "pattern", Color: []string{"FFF2CC"}, Pattern: 1},
		Border: []excelize.Border{
			{Type: "left", Color: "D9D9D9", Style: 1},
			{Type: "right", Color: "D9D9D9", Style: 1},
			{Type: "top", Color: "D9D9D9", Style: 1},
			{Type: "bottom", Color: "D9D9D9", Style: 1},
		},
	})

	// ---------- شیت جزئیات ----------
	headers := []string{"نام و نام‌خانوادگی", "تاریخ (ورود)", "ساعت ورود", "تاریخ (خروج)", "ساعت خروج", "مدت شیفت (ساعت)", "نوع ثبت", "توضیح"}
	for i, h := range headers {
		col, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(detailSheet, col, h)
		f.SetCellStyle(detailSheet, col, col, headerStyle)
	}
	widths := []float64{22, 14, 10, 14, 10, 14, 16, 26}
	for i, w := range widths {
		colName, _ := excelize.ColumnNumberToName(i + 1)
		f.SetColWidth(detailSheet, colName, colName, w)
	}

	now := time.Now()
	sortedShifts := make([]models.Shift, len(shifts))
	copy(sortedShifts, shifts)
	sort.Slice(sortedShifts, func(i, j int) bool { return sortedShifts[i].CheckIn.Before(sortedShifts[j].CheckIn) })

	monthlyTotals := map[string]map[int]float64{} // "1405/04" -> employeeID -> hours
	overallTotals := map[int]float64{}

	row := 2
	for _, sh := range sortedShifts {
		emp := employees[sh.EmployeeID]
		dur := sh.Duration(now)
		hours := dur.Hours()

		checkOutDateStr := "—"
		checkOutTimeStr := "شیفت باز است"
		if sh.CheckOut != nil {
			checkOutDateStr = jalali.DateString(*sh.CheckOut)
			checkOutTimeStr = jalali.TimeString(*sh.CheckOut)
		}
		regType := "زنده (خودکار)"
		if sh.Manual {
			regType = "ثبت دستی / با تاخیر"
		}

		values := []interface{}{
			emp.FullName(),
			jalali.DateString(sh.CheckIn),
			jalali.TimeString(sh.CheckIn),
			checkOutDateStr,
			checkOutTimeStr,
			fmt.Sprintf("%.2f", hours),
			regType,
			sh.Note,
		}
		st := cellStyle
		if sh.Manual {
			st = manualStyle
		}
		for i, v := range values {
			col, _ := excelize.CoordinatesToCellName(i+1, row)
			f.SetCellValue(detailSheet, col, v)
			f.SetCellStyle(detailSheet, col, col, st)
		}
		row++

		jy, jm, _ := jalali.YMD(sh.CheckIn)
		key := fmt.Sprintf("%04d/%02d", jy, jm)
		if monthlyTotals[key] == nil {
			monthlyTotals[key] = map[int]float64{}
		}
		monthlyTotals[key][sh.EmployeeID] += hours
		overallTotals[sh.EmployeeID] += hours
	}
	f.SetPanes(detailSheet, &excelize.Panes{Freeze: true, Split: false, XSplit: 0, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})

	// ---------- شیت خلاصه ----------
	f.SetCellValue(summarySheet, "A1", "نام و نام‌خانوادگی")
	f.SetCellValue(summarySheet, "B1", "ماه (شمسی)")
	f.SetCellValue(summarySheet, "C1", "جمع ساعت کارکرد")
	for _, cell := range []string{"A1", "B1", "C1"} {
		f.SetCellStyle(summarySheet, cell, cell, headerStyle)
	}
	f.SetColWidth(summarySheet, "A", "A", 24)
	f.SetColWidth(summarySheet, "B", "B", 16)
	f.SetColWidth(summarySheet, "C", "C", 18)

	type rowKey struct {
		month string
		empID int
	}
	keys := make([]rowKey, 0)
	for month, m := range monthlyTotals {
		for empID := range m {
			keys = append(keys, rowKey{month, empID})
		}
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].month != keys[j].month {
			return keys[i].month < keys[j].month
		}
		return keys[i].empID < keys[j].empID
	})

	r := 2
	firstDataRow := r
	for _, k := range keys {
		emp := employees[k.empID]
		hours := monthlyTotals[k.month][k.empID]
		f.SetCellValue(summarySheet, fmt.Sprintf("A%d", r), emp.FullName())
		f.SetCellValue(summarySheet, fmt.Sprintf("B%d", r), jalali.ToPersianDigits(k.month))
		f.SetCellValue(summarySheet, fmt.Sprintf("C%d", r), fmt.Sprintf("%.2f", hours))
		for _, col := range []string{"A", "B", "C"} {
			cell := fmt.Sprintf("%s%d", col, r)
			f.SetCellStyle(summarySheet, cell, cell, cellStyle)
		}
		r++
	}
	lastDataRow := r - 1

	// نمودار ساده ستونی از ساعت کار هر نفر (بر اساس ماه)
	if lastDataRow >= firstDataRow {
		if err := f.AddChart(summarySheet, "E2", &excelize.Chart{
			Type: excelize.Col,
			Series: []excelize.ChartSeries{
				{
					Name:       summarySheet + "!$C$1",
					Categories: fmt.Sprintf("%s!$A$%d:$A$%d", summarySheet, firstDataRow, lastDataRow),
					Values:     fmt.Sprintf("%s!$C$%d:$C$%d", summarySheet, firstDataRow, lastDataRow),
				},
			},
			Title:  []excelize.RichTextRun{{Text: "ساعت کارکرد به تفکیک نفر و ماه"}},
			Legend: excelize.ChartLegend{Position: "bottom"},
			XAxis:  excelize.ChartAxis{Font: excelize.Font{Size: 9}},
		}); err != nil {
			return nil, err
		}
	}

	f.SetActiveSheet(0)
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func boolPtr(b bool) *bool { return &b }
