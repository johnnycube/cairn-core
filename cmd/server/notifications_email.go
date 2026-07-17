package main

import (
	"log/slog"
	"net/http"

	"github.com/johnnycube/cairn-core/internal/port"
)

// mountNotificationEmailTest exposes a "send me a test email" action so a user
// can verify the email channel works (right relay, right address) before
// relying on it. Sends to the caller's own account email.
//
//	POST /api/notifications/test-email
func mountNotificationEmailTest(mux *http.ServeMux, app *App, logger *slog.Logger) {
	mux.HandleFunc("POST /api/notifications/test-email", func(w http.ResponseWriter, r *http.Request) {
		userID, ok := resolveSessionUser(r, app)
		if !ok {
			http.Error(w, "unauthenticated", http.StatusUnauthorized)
			return
		}
		if app.Email == nil {
			http.Error(w, "email is not configured on this instance", http.StatusServiceUnavailable)
			return
		}
		user, err := app.Users.GetUser(r.Context(), userID)
		if err != nil {
			http.Error(w, "user lookup failed", http.StatusInternalServerError)
			return
		}
		if user.Email == "" {
			http.Error(w, "your account has no email address", http.StatusBadRequest)
			return
		}

		err = app.Email.Send(r.Context(), port.EmailMessage{
			To:      user.Email,
			Subject: "Cairn — test email",
			TextBody: "This is a test email from your Cairn instance.\n\n" +
				"If you received this, the email channel is working and notifications " +
				"can be delivered to this address.\n\n— Cairn",
			HTMLBody: "<p>This is a <strong>test email</strong> from your Cairn instance.</p>" +
				"<p>If you received this, the email channel is working and notifications " +
				"can be delivered to this address.</p><p>— Cairn</p>",
		})
		if err != nil {
			logger.Warn("test email send failed", "user", userID, "error", err)
			http.Error(w, "send failed: "+err.Error(), http.StatusBadGateway)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"sent": true, "to": user.Email})
	})

	logger.Info("notification test-email endpoint mounted")
}
