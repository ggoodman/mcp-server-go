package sessions

type Broker interface {
	// ConsumeMessages(ctx context.Context, lastEventID string, writeMsgFn MessageHandlerFunction) error
	// WriteMessage(ctx context.Context, msg OutgoingMessageEnvelope) error
}
