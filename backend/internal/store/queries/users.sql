-- name: GetUser :one
SELECT id, auth_provider, email, display_name, goal, level,
       default_scoring_level, target_accent, daily_goal_items,
       timezone, is_guest, onboarding_completed,
       consent_store_recordings, consent_improve_product,
       notif_practice_reminder, notif_streak_reminder, reminder_time,
       created_at, updated_at, deleted_at
FROM users
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetUserByEmail :one
SELECT id, auth_provider, email, password_hash, display_name,
       default_scoring_level, is_guest, onboarding_completed, deleted_at
FROM users
WHERE email = $1 AND auth_provider = 'email' AND deleted_at IS NULL;

-- name: GetUserBySocialID :one
SELECT id, auth_provider, email, display_name, is_guest, deleted_at
FROM users
WHERE auth_provider = $1 AND external_auth_id = $2 AND deleted_at IS NULL;

-- name: CreateUser :one
INSERT INTO users (id, auth_provider, email, external_auth_id, password_hash, display_name, is_guest)
VALUES ($1, $2, $3, $4, $5, $6, $7)
RETURNING id;

-- name: UpdateUserProfile :one
UPDATE users SET
  goal                  = COALESCE(NULLIF($2,''), goal),
  level                 = COALESCE(NULLIF($3,''), level),
  target_accent         = COALESCE(NULLIF($4,''), target_accent),
  daily_goal_items      = COALESCE($5, daily_goal_items),
  default_scoring_level = COALESCE(NULLIF($6,''), default_scoring_level),
  display_name          = COALESCE($7, display_name),
  timezone              = COALESCE($8, timezone)
WHERE id = $1 AND deleted_at IS NULL
RETURNING id, auth_provider, email, display_name, goal, level,
          default_scoring_level, target_accent, daily_goal_items, timezone,
          is_guest, onboarding_completed, created_at;

-- name: SoftDeleteUser :exec
UPDATE users SET deleted_at = now() WHERE id = $1 AND deleted_at IS NULL;

-- name: GetPrivacy :one
SELECT consent_improve_product, consent_store_recordings
FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: UpdatePrivacy :exec
UPDATE users SET
  consent_improve_product  = COALESCE($2, consent_improve_product),
  consent_store_recordings = COALESCE($3, consent_store_recordings)
WHERE id = $1 AND deleted_at IS NULL;

-- name: GetNotifPrefs :one
SELECT notif_practice_reminder,
       TO_CHAR(reminder_time, 'HH24:MI') AS reminder_time_str,
       notif_streak_reminder
FROM users WHERE id = $1 AND deleted_at IS NULL;

-- name: UpsertDevice :exec
INSERT INTO user_devices (user_id, platform, push_token, last_seen_at)
VALUES ($1, $2, $3, now())
ON CONFLICT (user_id, push_token) DO UPDATE SET last_seen_at = now();

-- name: CreateAuthSession :exec
INSERT INTO auth_sessions (id, user_id, refresh_hash, expires_at)
VALUES ($1, $2, $3, $4);

-- name: GetAuthSession :one
SELECT id, user_id, revoked_at FROM auth_sessions
WHERE id = $1 AND user_id = $2;

-- name: RevokeAuthSession :exec
UPDATE auth_sessions SET revoked_at = now() WHERE id = $1;
