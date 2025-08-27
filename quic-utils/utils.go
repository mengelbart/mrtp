package quicutils

type Role bool

const (
	RoleServer Role = true
	RoleClient Role = false
)

func (r Role) String() string {
	if r {
		return "Server"
	} else {
		return "client"
	}
}
