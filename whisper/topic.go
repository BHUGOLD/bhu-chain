package whisper

import "github.com/bhu/bhu-chain/crypto"

type Topic [4]byte

func NewTopic(data []byte) Topic {
	prefix := [4]byte{}
	copy(prefix[:], crypto.Sha3(data)[:4])
	return Topic(prefix)
}

func NewTopics(data ...[]byte) []Topic {
	topics := make([]Topic, len(data))
	for i, element := range data {
		topics[i] = NewTopic(element)
	}
	return topics
}

func NewTopicFromString(data string) Topic {
	return NewTopic([]byte(data))
}

func NewTopicsFromStrings(data ...string) []Topic {
	topics := make([]Topic, len(data))
	for i, element := range data {
		topics[i] = NewTopicFromString(element)
	}
	return topics
}

func (self *Topic) String() string {
	return string(self[:])
}

type topicMatcher struct {
	conditions []map[Topic]struct{}
}

func newTopicMatcher(topics ...[]Topic) *topicMatcher {
	matcher := make([]map[Topic]struct{}, len(topics))
	for i, condition := range topics {
		matcher[i] = make(map[Topic]struct{})
		for _, topic := range condition {
			matcher[i][topic] = struct{}{}
		}
	}
	return &topicMatcher{conditions: matcher}
}

func newTopicMatcherFromBinary(data ...[][]byte) *topicMatcher {
	topics := make([][]Topic, len(data))
	for i, condition := range data {
		topics[i] = NewTopics(condition...)
	}
	return newTopicMatcher(topics...)
}

func newTopicMatcherFromStrings(data ...[]string) *topicMatcher {
	topics := make([][]Topic, len(data))
	for i, condition := range data {
		topics[i] = NewTopicsFromStrings(condition...)
	}
	return newTopicMatcher(topics...)
}

func (self *topicMatcher) Matches(topics []Topic) bool {

	if len(self.conditions) > len(topics) {
		return false
	}

	for i := 0; i < len(topics) && i < len(self.conditions); i++ {
		if len(self.conditions[i]) > 0 {
			if _, ok := self.conditions[i][topics[i]]; !ok {
				return false
			}
		}
	}
	return true
}
