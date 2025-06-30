package model

type Error struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type Channel struct {
	ID int `json:"id"`
}
