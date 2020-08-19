package whisper

import (
	"crypto/ecdsa"

	"github.com/bhu/bhu-chain/event/filter"
)

type Filter struct {
	To     *ecdsa.PublicKey
	From   *ecdsa.PublicKey
	Topics [][]Topic
	Fn     func(msg *Message)
}

func NewFilterTopics(data ...[][]byte) [][]Topic {
	filter := make([][]Topic, len(data))
	for i, condition := range data {

		if len(condition) == 1 && len(condition[0]) == 0 {
			filter[i] = []Topic{}
			continue
		}

		filter[i] = NewTopics(condition...)
	}
	return filter
}

func NewFilterTopicsFlat(data ...[]byte) [][]Topic {
	filter := make([][]Topic, len(data))
	for i, element := range data {

		filter[i] = make([]Topic, 0, 1)
		if len(element) > 0 {
			filter[i] = append(filter[i], NewTopic(element))
		}
	}
	return filter
}

func NewFilterTopicsFromStrings(data ...[]string) [][]Topic {
	filter := make([][]Topic, len(data))
	for i, condition := range data {

		if len(condition) == 1 && condition[0] == "" {
			filter[i] = []Topic{}
			continue
		}

		filter[i] = NewTopicsFromStrings(condition...)
	}
	return filter
}

func NewFilterTopicsFromStringsFlat(data ...string) [][]Topic {
	filter := make([][]Topic, len(data))
	for i, element := range data {

		filter[i] = make([]Topic, 0, 1)
		if element != "" {
			filter[i] = append(filter[i], NewTopicFromString(element))
		}
	}
	return filter
}

type filterer struct {
	to      string
	from    string
	matcher *topicMatcher
	fn      func(data interface{})
}

func (self filterer) Compare(f filter.Filter) bool {
	filter := f.(filterer)

	if len(self.to) > 0 && self.to != filter.to {
		return false
	}
	if len(self.from) > 0 && self.from != filter.from {
		return false
	}

	topics := make([]Topic, len(filter.matcher.conditions))
	for i, group := range filter.matcher.conditions {

		for topics[i], _ = range group {
			break
		}
	}
	if !self.matcher.Matches(topics) {
		return false
	}
	return true
}

func (self filterer) Trigger(data interface{}) {
	self.fn(data)
}
