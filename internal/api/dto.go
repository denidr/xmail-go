package api

import (
	"time"

	"xmail/internal/account"
)

// connConfigDTO is the JSON shape of account.ConnectionConfig.
type connConfigDTO struct {
	Host    string `json:"host"`
	Port    int    `json:"port"`
	TLSMode string `json:"tls_mode"`
}

func (c *connConfigDTO) toDomain() *account.ConnectionConfig {
	if c == nil {
		return nil
	}
	return &account.ConnectionConfig{Host: c.Host, Port: c.Port, TLSMode: account.TLSMode(c.TLSMode)}
}

func connConfigFromDomain(c *account.ConnectionConfig) *connConfigDTO {
	if c == nil {
		return nil
	}
	return &connConfigDTO{Host: c.Host, Port: c.Port, TLSMode: string(c.TLSMode)}
}

// accountRequest is the JSON body accepted by POST/PUT /accounts.
// Password is required on create; on update, a nil/omitted Password
// leaves the stored credential unchanged.
type accountRequest struct {
	Name     string         `json:"name"`
	Email    string         `json:"email"`
	Username string         `json:"username"`
	Password *string        `json:"password"`
	SMTP     *connConfigDTO `json:"smtp"`
	IMAP     *connConfigDTO `json:"imap"`
	POP3     *connConfigDTO `json:"pop3"`
}

func (r accountRequest) toDomain(id string) account.Account {
	return account.Account{
		ID:       id,
		Name:     r.Name,
		Email:    r.Email,
		Username: r.Username,
		SMTP:     r.SMTP.toDomain(),
		IMAP:     r.IMAP.toDomain(),
		POP3:     r.POP3.toDomain(),
	}
}

// accountResponse is the JSON shape returned for an account. It never
// includes the credential (never expose secrets via API responses).
type accountResponse struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Email     string         `json:"email"`
	Username  string         `json:"username"`
	SMTP      *connConfigDTO `json:"smtp"`
	IMAP      *connConfigDTO `json:"imap"`
	POP3      *connConfigDTO `json:"pop3"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

func accountResponseFromDomain(a account.Account) accountResponse {
	return accountResponse{
		ID:        a.ID,
		Name:      a.Name,
		Email:     a.Email,
		Username:  a.Username,
		SMTP:      connConfigFromDomain(a.SMTP),
		IMAP:      connConfigFromDomain(a.IMAP),
		POP3:      connConfigFromDomain(a.POP3),
		CreatedAt: a.CreatedAt.Format(time.RFC3339),
		UpdatedAt: a.UpdatedAt.Format(time.RFC3339),
	}
}
