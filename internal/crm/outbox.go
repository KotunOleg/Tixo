package crm

const TopicCRMContact = "crm.contact"

type ContactEvent struct {
	UserID int64  `json:"user_id"`
	Kind   string `json:"kind"`
}
