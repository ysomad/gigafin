package ui

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/i18n"
	"github.com/ysomad/gigafin/internal/ui/pages"
	"github.com/ysomad/gigafin/internal/ui/view"
)

func (u *UI) loginPage(w http.ResponseWriter, r *http.Request) {
	//nolint:errcheck // render logs its own errors
	_ = u.render(w, r, http.StatusOK, pages.Login, view.Page{Title: "Sign in", Data: view.Login{}})
}

func (u *UI) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, _maxForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	login, pass := r.PostFormValue("login"), r.PostFormValue("password")
	lang := i18n.Match(r.Header.Get("Accept-Language"))
	errs := view.FieldsError{}
	if strings.TrimSpace(login) == "" {
		errs["login"] = u.dicts.T(lang, "required")
	}
	if pass == "" {
		errs["password"] = u.dicts.T(lang, "required")
	}
	if len(errs) > 0 {
		//nolint:errcheck // render logs its own errors
		_ = u.render(w, r, http.StatusUnprocessableEntity, pages.Login, view.Page{
			Title: "Sign in", Error: errs, Form: "login", Data: view.Login{Login: login},
		})
		return
	}
	token, _, err := u.svc.Auth.Login(r.Context(), login, pass, r.UserAgent())
	if err != nil && !isUserError(err) {
		u.serverError(w, r, err)
		return
	}
	if err != nil {
		if errors.Is(err, errors.ErrUnauthorized) {
			// for fail2ban and alerts; behind a proxy the address is the proxy's
			u.log.WarnContext(r.Context(), "login failed",
				slog.String("login", login), slog.String("remote_addr", r.RemoteAddr))
		}
		//nolint:errcheck // render logs its own errors
		_ = u.render(w, r, http.StatusUnprocessableEntity, pages.Login, view.Page{
			Title: "Sign in", Error: err, Form: "login", Data: view.Login{Login: login},
		})
		return
	}
	// the server ends idle sessions earlier, see domain.SessionLifetime
	http.SetCookie(w, u.cookie(_sessionCookie, token, int(u.sessionTTL.Seconds())))
	redirect(w, r, "/")
}

func (u *UI) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(u.cookie(_sessionCookie, "", 0).Name); err == nil {
		if err := u.svc.Auth.Logout(r.Context(), c.Value); err != nil {
			u.serverError(w, r, err)
			return
		}
	}
	http.SetCookie(w, u.cookie(_sessionCookie, "", -1))
	redirect(w, r, "/login")
}
