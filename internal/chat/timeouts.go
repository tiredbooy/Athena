package chat

import "time"

// TurnTimeout bounds the complete retrieval, model, and action cycle. Local
// models can take several minutes to load or answer, especially after the
// native-tool fallback has already made one rejected request. The UI still
// lets the user cancel immediately with Esc.
const TurnTimeout = 5 * time.Minute
