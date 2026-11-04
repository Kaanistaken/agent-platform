package api

import (
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/kaang/vodafone-case/backend/internal/auth"
	"github.com/kaang/vodafone-case/backend/internal/crypto"
	"github.com/kaang/vodafone-case/backend/internal/db"
	"github.com/kaang/vodafone-case/backend/internal/provider"
	"github.com/kaang/vodafone-case/backend/internal/run"
	"github.com/kaang/vodafone-case/backend/internal/tool"
)

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		s.handleError(w, r, err)
		return
	}
	token, err := auth.Login(r.Context(), s.DB.W, strings.TrimSpace(body.Username), body.Password)
	if errors.Is(err, auth.ErrBadCredentials) {
		writeError(w, http.StatusUnauthorized, err.Error())
		return
	}
	if err != nil {
		s.handleError(w, r, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: SessionCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: strings.HasPrefix(s.Origin, "https://"), MaxAge: int(auth.SessionTTL.Seconds()),
	})
	u, _ := auth.Authenticate(r.Context(), s.DB.W, token)
	writeJSON(w, http.StatusOK, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(SessionCookie); err == nil {
		auth.Logout(r.Context(), s.DB.W, c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: SessionCookie, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, u auth.User) error {
	writeJSON(w, http.StatusOK, u)
	return nil
}

func (s *Server) models(w http.ResponseWriter, r *http.Request, u auth.User) error {
	writeJSON(w, http.StatusOK, provider.Models)
	return nil
}

// --- model keys (R7, D12, D18) ---

func (s *Server) listModelKeys(w http.ResponseWriter, r *http.Request, u auth.User) error {
	out := []map[string]any{}
	for _, p := range []string{"openai", "anthropic"} {
		var updated sql.NullString
		s.DB.R.QueryRowContext(r.Context(), "SELECT updated_at FROM user_model_keys WHERE user_id = ? AND provider = ?",
			u.ID, p).Scan(&updated)
		out = append(out, map[string]any{"provider": p, "set": updated.Valid, "updated_at": updated.String})
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

func validProvider(p string) bool { return p == "openai" || p == "anthropic" }

func (s *Server) putModelKey(w http.ResponseWriter, r *http.Request, u auth.User) error {
	prov := r.PathValue("provider")
	if !validProvider(prov) {
		return notFound()
	}
	var body struct {
		Key string `json:"key"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	key := strings.TrimSpace(body.Key)
	if key == "" || len(key) > 512 || strings.ContainsAny(key, " \r\n\t") {
		return badRequest("the key is empty or not valid")
	}
	ct, nonce, err := s.Box.Seal([]byte(key), crypto.ModelKeyAAD(u.ID, prov))
	if err != nil {
		return err
	}
	var changed []int64
	err = s.tx(r, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO user_model_keys (user_id, provider, ciphertext, nonce, key_version, aad_version, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (user_id, provider) DO UPDATE SET ciphertext = excluded.ciphertext,
			nonce = excluded.nonce, key_version = excluded.key_version, aad_version = excluded.aad_version, updated_at = excluded.updated_at`,
			u.ID, prov, ct, nonce, crypto.KeyVersion, crypto.AADVersion, db.Now()); err != nil {
			return err
		}
		changed, err = run.OnModelKeyChanged(r.Context(), tx, u.ID, prov, false)
		return err
	})
	if err != nil {
		return err
	}
	s.Engine.Notify(changed)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteModelKey(w http.ResponseWriter, r *http.Request, u auth.User) error {
	prov := r.PathValue("provider")
	if !validProvider(prov) {
		return notFound()
	}
	var changed []int64
	err := s.tx(r, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(r.Context(), "DELETE FROM user_model_keys WHERE user_id = ? AND provider = ?", u.ID, prov); err != nil {
			return err
		}
		var err error
		changed, err = run.OnModelKeyChanged(r.Context(), tx, u.ID, prov, true)
		return err
	})
	if err != nil {
		return err
	}
	s.Engine.Notify(changed)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// --- tool secrets (D32, D54): per user, bound to a host, write-only ---

var secretNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)

func (s *Server) listSecrets(w http.ResponseWriter, r *http.Request, u auth.User) error {
	rows, err := s.DB.R.QueryContext(r.Context(), "SELECT id, name, allowed_host, updated_at FROM user_secrets WHERE user_id = ? ORDER BY name", u.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id int64
		var name, host, updated string
		if err := rows.Scan(&id, &name, &host, &updated); err != nil {
			return err
		}
		out = append(out, map[string]any{"id": id, "name": name, "allowed_host": host, "updated_at": updated})
	}
	writeJSON(w, http.StatusOK, out)
	return rows.Err()
}

func validSecretValue(v string) bool {
	return v != "" && len(v) <= 4096 && !strings.ContainsAny(v, "\r\n")
}

func (s *Server) createSecret(w http.ResponseWriter, r *http.Request, u auth.User) error {
	var body struct {
		Name        string `json:"name"`
		AllowedHost string `json:"allowed_host"`
		Value       string `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	if !secretNameRe.MatchString(body.Name) {
		return badRequest("secret name must be letters, digits and '_' (e.g. GITHUB_TOKEN)")
	}
	host, err := tool.NormalizeHost(body.AllowedHost)
	if err != nil {
		return badRequest("allowed host: " + err.Error())
	}
	value := strings.TrimSpace(body.Value)
	if !validSecretValue(value) {
		return badRequest("the secret value is empty or not valid")
	}
	var id int64
	var changed []int64
	err = s.tx(r, func(tx *sql.Tx) error {
		var n int
		tx.QueryRowContext(r.Context(), "SELECT COUNT(*) FROM user_secrets WHERE user_id = ? AND name = ?", u.ID, body.Name).Scan(&n)
		if n > 0 {
			return conflict("you already have a secret with this name; delete it first to change its host")
		}
		res, err := tx.ExecContext(r.Context(), `INSERT INTO user_secrets (user_id, name, allowed_host, ciphertext, nonce, key_version, aad_version, updated_at)
			VALUES (?, ?, ?, x'', x'', ?, ?, ?)`, u.ID, body.Name, host, crypto.KeyVersion, crypto.AADVersion, db.Now())
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
		ct, nonce, err := s.Box.Seal([]byte(value), crypto.SecretAAD(id, u.ID))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(r.Context(), "UPDATE user_secrets SET ciphertext = ?, nonce = ? WHERE id = ?", ct, nonce, id); err != nil {
			return err
		}
		changed, err = run.OnSecretAdded(r.Context(), tx, u.ID, id, body.Name, host)
		return err
	})
	if err != nil {
		return err
	}
	s.Engine.Notify(changed)
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "name": body.Name, "allowed_host": host})
	return nil
}

func (s *Server) updateSecret(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var body struct {
		Value string `json:"value"`
	}
	if err := readJSON(r, &body); err != nil {
		return err
	}
	value := strings.TrimSpace(body.Value)
	if !validSecretValue(value) {
		return badRequest("the secret value is empty or not valid")
	}
	ct, nonce, err := s.Box.Seal([]byte(value), crypto.SecretAAD(id, u.ID))
	if err != nil {
		return err
	}
	res, err := s.DB.W.ExecContext(r.Context(), "UPDATE user_secrets SET ciphertext = ?, nonce = ?, updated_at = ? WHERE id = ? AND user_id = ?",
		ct, nonce, db.Now(), id, u.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return notFound()
	}
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) deleteSecret(w http.ResponseWriter, r *http.Request, u auth.User) error {
	id, err := pathID(r, "id")
	if err != nil {
		return err
	}
	var changed []int64
	err = s.tx(r, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(r.Context(), "DELETE FROM user_secrets WHERE id = ? AND user_id = ?", id, u.ID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return notFound()
		}
		changed, err = run.OnSecretDeleted(r.Context(), tx, u.ID, id)
		return err
	})
	if err != nil {
		return err
	}
	s.Engine.Notify(changed)
	w.WriteHeader(http.StatusNoContent)
	return nil
}

func (s *Server) tx(r *http.Request, fn func(tx *sql.Tx) error) error {
	tx, err := s.DB.W.BeginTx(r.Context(), nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}
