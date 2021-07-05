package chat

// a message should floodfill to every known device if it's about you,
// or if it's from anyone in a chat to anyone else in the chat
// assume something was seen if it was sent before the last ok health check
