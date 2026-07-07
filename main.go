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
