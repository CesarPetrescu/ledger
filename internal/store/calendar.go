package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

type CalendarAccount struct {
	ServerURL          string
	Username           string
	PasswordCiphertext []byte
	SelectedCalendars  []string
	ConnectedAt        time.Time
	UpdatedAt          time.Time
}

func (db *DB) PutCalendarAccount(ctx context.Context, account CalendarAccount) error {
	_, err := db.Pool.Exec(ctx, `INSERT INTO calendar_account(singleton,server_url,username,password_ciphertext,selected_calendars)
VALUES(true,$1,$2,$3,$4)
ON CONFLICT(singleton) DO UPDATE SET server_url=EXCLUDED.server_url,username=EXCLUDED.username,
 password_ciphertext=EXCLUDED.password_ciphertext,selected_calendars=EXCLUDED.selected_calendars,updated_at=now()`,
		account.ServerURL, account.Username, account.PasswordCiphertext, account.SelectedCalendars)
	return err
}

func (db *DB) GetCalendarAccount(ctx context.Context) (CalendarAccount, error) {
	var account CalendarAccount
	err := db.Pool.QueryRow(ctx, `SELECT server_url,username,password_ciphertext,selected_calendars,connected_at,updated_at
FROM calendar_account WHERE singleton`).Scan(&account.ServerURL, &account.Username, &account.PasswordCiphertext, &account.SelectedCalendars, &account.ConnectedAt, &account.UpdatedAt)
	return account, err
}

func (db *DB) SetSelectedCalendars(ctx context.Context, paths []string) error {
	command, err := db.Pool.Exec(ctx, `UPDATE calendar_account SET selected_calendars=$1,updated_at=now() WHERE singleton`, paths)
	if err == nil && command.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return err
}

func (db *DB) DeleteCalendarAccount(ctx context.Context) error {
	_, err := db.Pool.Exec(ctx, `DELETE FROM calendar_account WHERE singleton`)
	return err
}

func (db *DB) NotifyAdminEvent(ctx context.Context, entity string) error {
	_, err := db.Pool.Exec(ctx, `SELECT pg_notify('ledger_admin_event', json_build_object('entity',$1::text)::text)`, entity)
	return err
}
