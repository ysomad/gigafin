// Package auth serves signing in and out.
package auth

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/ysomad/gigafin/internal/domain"
	"github.com/ysomad/gigafin/internal/errors"
	"github.com/ysomad/gigafin/internal/ui/kit"
)

const _maxForm = 1 << 20

// Handler serves signing in and out.
type Handler struct {
	app *kit.App
}

// New returns the handler of app.
func New(app *kit.App) *Handler {
	return &Handler{app: app}
}

// Register adds the routes, open to signed-out users, to mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /login", h.loginPage)
	mux.HandleFunc("POST /login", h.login)
	mux.HandleFunc("POST /logout", h.logout)
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	err := kit.Render(h.app, w, r, http.StatusOK, loginPage, kit.Page{Title: "Sign in"}, loginData{}, kit.Failure{})
	if err != nil {
		h.app.ServerError(w, r, err)
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, _maxForm)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	login, pass := r.PostFormValue("login"), r.PostFormValue("password")
	errs := kit.FieldsError{}
	if strings.TrimSpace(login) == "" {
		errs["login"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if pass == "" {
		errs["password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		err := kit.Render(h.app, w, r, http.StatusUnprocessableEntity, loginPage, kit.Page{Title: "Sign in"},
			loginData{Login: login}, kit.Failure{Err: errs, Form: "login"})
		if err != nil {
			h.app.ServerError(w, r, err)
		}
		return
	}
	token, _, err := h.app.Svc.Auth.Login(r.Context(), login, pass, r.UserAgent())
	if err != nil && !kit.IsUserError(err) {
		h.app.ServerError(w, r, err)
		return
	}
	if err != nil {
		if errors.Is(err, errors.ErrUnauthorized) {
			// for fail2ban and alerts; behind a proxy the address is the proxy's
			h.app.Log.WarnContext(r.Context(), "login failed",
				slog.String("login", login), slog.String("remote_addr", r.RemoteAddr))
		}
		err := kit.Render(h.app, w, r, http.StatusUnprocessableEntity, loginPage, kit.Page{Title: "Sign in"},
			loginData{Login: login}, kit.Failure{Err: err, Form: "login"})
		if err != nil {
			h.app.ServerError(w, r, err)
		}
		return
	}
	// the server ends idle sessions earlier, see domain.SessionLifetime
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, token, int(h.app.SessionTTL.Seconds())))
	kit.Redirect(w, r, "/")
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if token := h.app.SessionToken(r); token != "" {
		if err := h.app.Svc.Auth.Logout(r.Context(), token); err != nil {
			h.app.ServerError(w, r, err)
			return
		}
	}
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, "", -1))
	kit.Redirect(w, r, "/login")
}
