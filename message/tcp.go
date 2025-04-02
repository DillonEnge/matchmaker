package message

const (
	REQUEST_NEW_CLIENT RequestType = iota
	REQUEST_CREATE_LOBBY
	REQUEST_JOIN_LOBBY
	REQUEST_START_LOBBY
)

type RequestType int

func (r RequestType) String() string {
	switch int(r) {
	case 0:
		return "new_client"
	case 1:
		return "create_lobby"
	case 2:
		return "join_lobby"
	case 3:
		return "start_lobby"
	default:
		return ""
	}
}

type Request struct {
	Type RequestType
	Body []byte
}

type NewClientBody struct{}

type NewClientResponse struct {
	SessionID string
}

type CreateLobbyBody struct {
	SessionID string
}

type CreateLobbyResponse struct {
	LobbyCode string
}

type JoinLobbyBody struct {
	LobbyCode string
	SessionID string
}

type JoinLobbyResponse struct {
}

type StartLobbyBody struct {
	LobbyCode string
	SessionID string
}

type StartLobbyResponse struct {
}
