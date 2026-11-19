package tgbot

// NotifyAdmins sends a one-off plain message to every configured admin chat.
// Unlike the live notices it never edits or folds into another card: each
// call is its own message. It is a no-op while the bot is not running.
func (t *Tgbot) NotifyAdmins(text string) {
	if !t.IsRunning() || text == "" {
		return
	}
	for _, adminID := range adminSnapshot() {
		t.sendNoticeReturningID(adminID, text)
	}
}
