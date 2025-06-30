package memory

import "github.com/mengelbart/mrtp/internal/model"

type ChannelList struct {
}

func (l *ChannelList) CreateChannel() (ID int) {
	return 0
}

func (l *ChannelList) ListChannels() ([]*model.Channel, error) {
	return nil, nil
}
