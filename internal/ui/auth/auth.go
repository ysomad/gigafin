// Package auth serves signing in, with a second step for users with two-factor
// authentication on, and out.
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

// _challengeCookie carries the challenge of a sign-in waiting for a second-factor code.
const _challengeCookie = "gigafin_2fa"

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
	mux.HandleFunc("GET /login/2fa", h.twoFactorPage)
	mux.HandleFunc("POST /login/2fa", h.twoFactor)
	mux.HandleFunc("POST /logout", h.logout)
}

// loginForm is decoded from the sign-in form.
type loginForm struct {
	Login    string `form:"login"`
	Password string `form:"password"`
}

func (h *Handler) loginPage(w http.ResponseWriter, r *http.Request) {
	err := kit.Render(h.app, w, r, http.StatusOK, loginPage, kit.Page{Title: "Sign in"}, loginData{}, kit.Failure{})
	if err != nil {
		h.app.ServerError(w, r, err)
	}
}

func (h *Handler) login(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, _maxForm)
	var f loginForm
	fail := func(err error) {
		err = kit.Render(h.app, w, r, http.StatusUnprocessableEntity, loginPage, kit.Page{Title: "Sign in"},
			loginData{Login: f.Login}, kit.Failure{Err: err, Form: "login"})
		if err != nil {
			h.app.ServerError(w, r, err)
		}
	}
	if err := h.app.DecodeForm(r, &f); err != nil {
		fail(err)
		return
	}
	errs := kit.FieldsError{}
	if strings.TrimSpace(f.Login) == "" {
		errs["login"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if f.Password == "" {
		errs["password"] = domain.NewError(errors.ErrInvalid, "required")
	}
	if len(errs) > 0 {
		fail(errs)
		return
	}
	in, err := h.app.Svc.Auth.Login(r.Context(), f.Login, f.Password, r.UserAgent())
	if err != nil && !kit.IsUserError(err) {
		h.app.ServerError(w, r, err)
		return
	}
	if err != nil {
		if errors.Is(err, errors.ErrUnauthorized) {
			// for fail2ban and alerts; behind a proxy the address is the proxy's
			h.app.Log.WarnContext(r.Context(), "login failed",
				slog.String("login", f.Login), slog.String("remote_addr", r.RemoteAddr))
		}
		fail(err)
		return
	}
	if in.TwoFactor {
		http.SetCookie(w, h.app.Cookie(_challengeCookie, in.Token, int(domain.LoginChallengeTTL.Seconds())))
		kit.Redirect(w, r, "/login/2fa")
		return
	}
	// the server ends idle sessions earlier, see domain.SessionLifetime
	http.SetCookie(w, h.app.Cookie(kit.SessionCookie, in.Token, int(h.app.SessionTTL.Seconds())))
	kit.Redirect(w, r, "/")
}

func (h *Handler) twoFactorPage(w http.ResponseWriter, r *http.Request) {
	if h.app.CookieValue(r, _challengeCookie) == "" {
		kit.Redirect(w, r, "/login")
		return
	}
	err := kit.Render(h.app, w, r, http.StatusOK, twoFactorPage, kit.Page{Title: "Sign in"}, struct{}{}, kit.Failure{})
	if err != nil {
		h.app.ServerError(w, r, err)
	}
}

func (h *Handler) twoFactor(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, _maxForm)
	fail := func(err error) {
		err = kit.Render(h.app, w, r, http.StatusUnprocessableEntity, twoFactorPage, kit.Page{Title: "Sign in"},
			struct{}{}, kit.Failure{Err: kit.FieldsError{"code": err}, Form: "2fa"})
		if err != nil {
			h.app.ServerError(w, r, err)
		}
	}
	var f struct {
		Code string `form:"code"`
	}
	if err := h.app.DecodeForm(r, &f); err != nil {
		fail(err)
		return
	}
	code := strings.TrimSpace(f.Code)
	if code == "" {
		fail(domain.NewError(errors.ErrInvalid, "required"))
		return
	}
	token, err := h.app.Svc.Auth.LoginTwoFactor(r.Context(), h.app.CookieValue(r, _challengeCookie), code)
	switch {
	case errors.Is(err, domain.ErrLoginExpired):
		http.SetCookie(w, h.app.Cookie(_challengeCookie, "", -1))
		expired := domain.NewError(errors.ErrUnauthorized, "sign-in expired, sign in again")
		err := kit.Render(h.app, w, r, http.StatusUnprocessableEntity, loginPage, kit.Page{Title: "Sign in"},
			loginData{}, kit.Failure{Err: expired, Form: "login"})
		if err != nil {
			h.app.ServerError(w, r, err)
		}
		return
	case err != nil && !kit.IsUserError(err):
		h.app.ServerError(w, r, err)
		return
	case err != nil:
		// for fail2ban and alerts; behind a proxy the address is the proxy's
		h.app.Log.WarnContext(r.Context(), "second factor failed", slog.String("remote_addr", r.RemoteAddr))
		fail(err)
		return
	}
	http.SetCookie(w, h.app.Cookie(_challengeCookie, "", -1))
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
