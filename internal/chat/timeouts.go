package chat

import "time"

// TurnTimeout bounds the complete retrieval, model, and action cycle. Each
// action also gets its own shorter deadline in tools.Dispatcher.
const TurnTimeout = 2 * time.Minute
