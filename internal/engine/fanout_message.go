package engine

type fanoutMessage struct {
	SessionID string `json:"sess_id"`
	UserID    string `json:"user_id"`
	Msg       []byte `json:"msg"`
}
