package reddit

import "strings"

const minBodyLength = 20

// BuildText combines a link post's title with its body (selftext for posts, body for comments) 
// into the text that gets classified downstream. Comments have no title, so title is often empty.
func BuildText(title, selftext, body string) string {
	text := selftext
	if text == "" {
		text = body
	}
	if title != "" {
		text = title + "\n\n" + text
	}
	return text
}

// ShouldSkip reports whether a post/comment's text should be dropped before ever reaching DynamoDB: 
// removed/deleted placeholders, or anything under the minimum length.
func ShouldSkip(text string) (skip bool, reason string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "[deleted]" || trimmed == "[removed]" {
		return true, "removed_or_deleted"
	}
	if len(text) < minBodyLength {
		return true, "too_short"
	}
	return false, ""
}
