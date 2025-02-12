// Package "main" provides primitives to interact with the AsyncAPI specification.
//
// Code generated by github.com/lerenn/asyncapi-codegen version (devel) DO NOT EDIT.
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/lerenn/asyncapi-codegen/pkg/extensions"

	"github.com/google/uuid"
)

// AppSubscriber contains all handlers that are listening messages for App
type AppSubscriber interface {
	// PingRequestOperationReceived receive all Ping messages from Ping channel.
	PingRequestOperationReceived(ctx context.Context, msg PingMessage) error
}

// AppController is the structure that provides sending capabilities to the
// developer and and connect the broker with the App
type AppController struct {
	controller
}

// NewAppController links the App to the broker
func NewAppController(bc extensions.BrokerController, options ...ControllerOption) (*AppController, error) {
	// Check if broker controller has been provided
	if bc == nil {
		return nil, extensions.ErrNilBrokerController
	}

	// Create default controller
	controller := controller{
		broker:        bc,
		subscriptions: make(map[string]extensions.BrokerChannelSubscription),
		logger:        extensions.DummyLogger{},
		middlewares:   make([]extensions.Middleware, 0),
		errorHandler:  extensions.DefaultErrorHandler(),
	}

	// Apply options
	for _, option := range options {
		option(&controller)
	}

	return &AppController{controller: controller}, nil
}

func (c AppController) wrapMiddlewares(
	middlewares []extensions.Middleware,
	callback extensions.NextMiddleware,
) func(ctx context.Context, msg *extensions.BrokerMessage) error {
	var called bool

	// If there is no more middleware
	if len(middlewares) == 0 {
		return func(ctx context.Context, msg *extensions.BrokerMessage) error {
			// Call the callback if it exists and it has not been called already
			if callback != nil && !called {
				called = true
				return callback(ctx)
			}

			// Nil can be returned, as the callback has already been called
			return nil
		}
	}

	// Get the next function to call from next middlewares or callback
	next := c.wrapMiddlewares(middlewares[1:], callback)

	// Wrap middleware into a check function that will call execute the middleware
	// and call the next wrapped middleware if the returned function has not been
	// called already
	return func(ctx context.Context, msg *extensions.BrokerMessage) error {
		// Call the middleware and the following if it has not been done already
		if !called {
			// Create the next call with the context and the message
			nextWithArgs := func(ctx context.Context) error {
				return next(ctx, msg)
			}

			// Call the middleware and register it as already called
			called = true
			if err := middlewares[0](ctx, msg, nextWithArgs); err != nil {
				return err
			}

			// If next has already been called in middleware, it should not be executed again
			return nextWithArgs(ctx)
		}

		// Nil can be returned, as the next middleware has already been called
		return nil
	}
}

func (c AppController) executeMiddlewares(ctx context.Context, msg *extensions.BrokerMessage, callback extensions.NextMiddleware) error {
	// Wrap middleware to have 'next' function when calling them
	wrapped := c.wrapMiddlewares(c.middlewares, callback)

	// Execute wrapped middlewares
	return wrapped(ctx, msg)
}

func addAppContextValues(ctx context.Context, addr string) context.Context {
	ctx = context.WithValue(ctx, extensions.ContextKeyIsVersion, "1.0.0")
	ctx = context.WithValue(ctx, extensions.ContextKeyIsProvider, "app")
	return context.WithValue(ctx, extensions.ContextKeyIsChannel, addr)
}

// Close will clean up any existing resources on the controller
func (c *AppController) Close(ctx context.Context) {
	// Unsubscribing remaining channels
	c.UnsubscribeFromAllChannels(ctx)

	c.logger.Info(ctx, "Closed app controller")
}

// SubscribeToAllChannels will receive messages from channels where channel has
// no parameter on which the app is expecting messages. For channels with parameters,
// they should be subscribed independently.
func (c *AppController) SubscribeToAllChannels(ctx context.Context, as AppSubscriber) error {
	if as == nil {
		return extensions.ErrNilAppSubscriber
	}

	if err := c.SubscribeToPingRequestOperation(ctx, as.PingRequestOperationReceived); err != nil {
		return err
	}

	return nil
}

// UnsubscribeFromAllChannels will stop the subscription of all remaining subscribed channels
func (c *AppController) UnsubscribeFromAllChannels(ctx context.Context) {
	c.UnsubscribeFromPingRequestOperation(ctx)
}

// SubscribeToPingRequestOperation will receive Ping messages from Ping channel.
//
// Callback function 'fn' will be called each time a new message is received.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
// If you need support for other messages, please raise an issue.
func (c *AppController) SubscribeToPingRequestOperation(
	ctx context.Context,
	fn func(ctx context.Context, msg PingMessage) error,
) error {
	// Get channel address
	addr := "ping.v3"

	// Set context
	ctx = addAppContextValues(ctx, addr)
	ctx = context.WithValue(ctx, extensions.ContextKeyIsDirection, "reception")

	// Check if the controller is already subscribed
	_, exists := c.subscriptions[addr]
	if exists {
		err := fmt.Errorf("%w: controller is already subscribed on channel %q", extensions.ErrAlreadySubscribedChannel, addr)
		c.logger.Error(ctx, err.Error())
		return err
	}

	// Subscribe to broker channel
	sub, err := c.broker.Subscribe(ctx, addr)
	if err != nil {
		c.logger.Error(ctx, err.Error())
		return err
	}
	c.logger.Info(ctx, "Subscribed to channel")

	// Asynchronously listen to new messages and pass them to app receiver
	go func() {
		for {
			// Wait for next message
			acknowledgeableBrokerMessage, open := <-sub.MessagesChannel()

			// If subscription is closed and there is no more message
			// (i.e. uninitialized message), then exit the function
			if !open && acknowledgeableBrokerMessage.IsUninitialized() {
				return
			}

			// Set broker message to context
			ctx = context.WithValue(ctx, extensions.ContextKeyIsBrokerMessage, acknowledgeableBrokerMessage.String())

			// Execute middlewares before handling the message
			if err := c.executeMiddlewares(ctx, &acknowledgeableBrokerMessage.BrokerMessage, func(ctx context.Context) error {
				// Process message
				msg, err := brokerMessageToPingMessage(acknowledgeableBrokerMessage.BrokerMessage)
				if err != nil {
					return err
				}

				// Add correlation ID to context if it exists
				if id := msg.CorrelationID(); id != "" {
					ctx = context.WithValue(ctx, extensions.ContextKeyIsCorrelationID, id)
				}

				// Execute the subscription function
				if err := fn(ctx, msg); err != nil {
					return err
				}

				acknowledgeableBrokerMessage.Ack()

				return nil
			}); err != nil {
				c.errorHandler(ctx, addr, &acknowledgeableBrokerMessage, err)
				// On error execute the acknowledgeableBrokerMessage nack() function and
				// let the BrokerAcknowledgment decide what is the right nack behavior for the broker
				acknowledgeableBrokerMessage.Nak()
			}
		}
	}()

	// Add the cancel channel to the inside map
	c.subscriptions[addr] = sub

	return nil
}

// ReplyToPingRequestOperation is a helper function to
// reply to a Ping message with a Pong message on Pong channel.
func (c *AppController) ReplyToPingRequestOperation(ctx context.Context, recvMsg PingMessage, fn func(replyMsg *PongMessage)) error {
	// Create reply message
	replyMsg := NewPongMessage()
	replyMsg.SetAsResponseFrom(&recvMsg)

	// Execute callback function
	fn(&replyMsg)

	// Publish reply
	return c.SendAsReplyToPingRequestOperation(ctx, replyMsg)
}

// UnsubscribeFromPingRequestOperation will stop the reception of Ping messages from Ping channel.
// A timeout can be set in context to avoid blocking operation, if needed.
func (c *AppController) UnsubscribeFromPingRequestOperation(
	ctx context.Context,
) {
	// Get channel address
	addr := "ping.v3"

	// Check if there receivers for this channel
	sub, exists := c.subscriptions[addr]
	if !exists {
		return
	}

	// Set context
	ctx = addAppContextValues(ctx, addr)

	// Stop the subscription
	sub.Cancel(ctx)

	// Remove if from the receivers
	delete(c.subscriptions, addr)

	c.logger.Info(ctx, "Unsubscribed from channel")
}

// SendAsReplyToPingRequestOperation will send a Pong message on Pong channel.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
// If you need support for other messages, please raise an issue.
func (c *AppController) SendAsReplyToPingRequestOperation(
	ctx context.Context,
	msg PongMessage,
) error {
	// Set channel address
	addr := "pong.v3"

	// Set correlation ID if it does not exist
	if id := msg.CorrelationID(); id == "" {
		c.logger.Error(ctx, extensions.ErrNoCorrelationIDSet.Error())
		return extensions.ErrNoCorrelationIDSet

	}

	// Set context
	ctx = addAppContextValues(ctx, addr)
	ctx = context.WithValue(ctx, extensions.ContextKeyIsDirection, "publication")
	ctx = context.WithValue(ctx, extensions.ContextKeyIsCorrelationID, msg.CorrelationID())

	// Convert to BrokerMessage
	brokerMsg, err := msg.toBrokerMessage()
	if err != nil {
		return err
	}

	// Set broker message to context
	ctx = context.WithValue(ctx, extensions.ContextKeyIsBrokerMessage, brokerMsg.String())

	// Send the message on event-broker through middlewares
	return c.executeMiddlewares(ctx, &brokerMsg, func(ctx context.Context) error {
		return c.broker.Publish(ctx, addr, brokerMsg)
	})
}

// AsyncAPIVersion is the version of the used AsyncAPI document
const AsyncAPIVersion = "1.0.0"

// controller is the controller that will be used to communicate with the broker
// It will be used internally by AppController and UserController
type controller struct {
	// broker is the broker controller that will be used to communicate
	broker extensions.BrokerController
	// subscriptions is a map of all subscriptions
	subscriptions map[string]extensions.BrokerChannelSubscription
	// logger is the logger that will be used² to log operations on controller
	logger extensions.Logger
	// middlewares are the middlewares that will be executed when sending or
	// receiving messages
	middlewares []extensions.Middleware
	// handler to handle errors from consumers and middlewares
	errorHandler extensions.ErrorHandler
}

// ControllerOption is the type of the options that can be passed
// when creating a new Controller
type ControllerOption func(controller *controller)

// WithLogger attaches a logger to the controller
func WithLogger(logger extensions.Logger) ControllerOption {
	return func(controller *controller) {
		controller.logger = logger
	}
}

// WithMiddlewares attaches middlewares that will be executed when sending or receiving messages
func WithMiddlewares(middlewares ...extensions.Middleware) ControllerOption {
	return func(controller *controller) {
		controller.middlewares = middlewares
	}
}

// WithErrorHandler attaches a errorhandler to handle errors from subscriber functions
func WithErrorHandler(handler extensions.ErrorHandler) ControllerOption {
	return func(controller *controller) {
		controller.errorHandler = handler
	}
}

type MessageWithCorrelationID interface {
	CorrelationID() string
	SetCorrelationID(id string)
}

type Error struct {
	Channel string
	Err     error
}

func (e *Error) Error() string {
	return fmt.Sprintf("channel %q: err %v", e.Channel, e.Err)
}

// Message 'PingMessageFromPingChannel' reference another one at '#/components/messages/ping'.
// This should be fixed in a future version to allow message override.
// If you encounter this message, feel free to open an issue on this subject
// to let know that you need this functionnality.

// Message 'PongMessageFromPongChannel' reference another one at '#/components/messages/pong'.
// This should be fixed in a future version to allow message override.
// If you encounter this message, feel free to open an issue on this subject
// to let know that you need this functionnality.

// HeadersFromPingMessage is a schema from the AsyncAPI specification required in messages
type HeadersFromPingMessage struct {
	// Description: Correlation ID set by user
	CorrelationId *string `json:"correlationId"`
}

// PingMessagePayload is a schema from the AsyncAPI specification required in messages
type PingMessagePayload struct {
	Event *string `json:"event"`
}

// PingMessage is the message expected for 'PingMessage' channel.
type PingMessage struct {
	// Headers will be used to fill the message headers
	Headers HeadersFromPingMessage

	// Payload will be inserted in the message payload
	Payload PingMessagePayload
}

func NewPingMessage() PingMessage {
	var msg PingMessage

	// Set correlation ID
	u := uuid.New().String()
	msg.Headers.CorrelationId = &u

	return msg
}

// brokerMessageToPingMessage will fill a new PingMessage with data from generic broker message
func brokerMessageToPingMessage(bMsg extensions.BrokerMessage) (PingMessage, error) {
	var msg PingMessage

	// Unmarshal payload to expected message payload format
	err := json.Unmarshal(bMsg.Payload, &msg.Payload)
	if err != nil {
		return msg, err
	}

	// Get each headers from broker message
	for k, v := range bMsg.Headers {
		switch {
		case k == "correlationId": // Retrieving CorrelationId header
			h := string(v)
			msg.Headers.CorrelationId = &h
		default:
			// TODO: log unknown error
		}
	}

	// TODO: run checks on msg type

	return msg, nil
}

// toBrokerMessage will generate a generic broker message from PingMessage data
func (msg PingMessage) toBrokerMessage() (extensions.BrokerMessage, error) {
	// TODO: implement checks on message

	// Marshal payload to JSON
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return extensions.BrokerMessage{}, err
	}

	// Add each headers to broker message
	headers := make(map[string][]byte, 1)

	// Adding CorrelationId header
	if msg.Headers.CorrelationId != nil {
		headers["correlationId"] = []byte(*msg.Headers.CorrelationId)
	}

	return extensions.BrokerMessage{
		Headers: headers,
		Payload: payload,
	}, nil
}

// CorrelationID will give the correlation ID of the message, based on AsyncAPI spec
func (msg PingMessage) CorrelationID() string {
	if msg.Headers.CorrelationId != nil {
		return *msg.Headers.CorrelationId
	}

	return ""
}

// SetCorrelationID will set the correlation ID of the message, based on AsyncAPI spec
func (msg *PingMessage) SetCorrelationID(id string) {
	msg.Headers.CorrelationId = &id
}

// SetAsResponseFrom will correlate the message with the one passed in parameter.
// It will assign the 'req' message correlation ID to the message correlation ID,
// both specified in AsyncAPI spec.
func (msg *PingMessage) SetAsResponseFrom(req MessageWithCorrelationID) {
	id := req.CorrelationID()
	msg.Headers.CorrelationId = &id
}

// HeadersFromPongMessage is a schema from the AsyncAPI specification required in messages
type HeadersFromPongMessage struct {
	// Description: Correlation ID set by user
	CorrelationId *string `json:"correlationId"`
}

// PongMessagePayload is a schema from the AsyncAPI specification required in messages
type PongMessagePayload struct {
	Event *string `json:"event"`
}

// PongMessage is the message expected for 'PongMessage' channel.
type PongMessage struct {
	// Headers will be used to fill the message headers
	Headers HeadersFromPongMessage

	// Payload will be inserted in the message payload
	Payload PongMessagePayload
}

func NewPongMessage() PongMessage {
	var msg PongMessage

	// Set correlation ID
	u := uuid.New().String()
	msg.Headers.CorrelationId = &u

	return msg
}

// brokerMessageToPongMessage will fill a new PongMessage with data from generic broker message
func brokerMessageToPongMessage(bMsg extensions.BrokerMessage) (PongMessage, error) {
	var msg PongMessage

	// Unmarshal payload to expected message payload format
	err := json.Unmarshal(bMsg.Payload, &msg.Payload)
	if err != nil {
		return msg, err
	}

	// Get each headers from broker message
	for k, v := range bMsg.Headers {
		switch {
		case k == "correlationId": // Retrieving CorrelationId header
			h := string(v)
			msg.Headers.CorrelationId = &h
		default:
			// TODO: log unknown error
		}
	}

	// TODO: run checks on msg type

	return msg, nil
}

// toBrokerMessage will generate a generic broker message from PongMessage data
func (msg PongMessage) toBrokerMessage() (extensions.BrokerMessage, error) {
	// TODO: implement checks on message

	// Marshal payload to JSON
	payload, err := json.Marshal(msg.Payload)
	if err != nil {
		return extensions.BrokerMessage{}, err
	}

	// Add each headers to broker message
	headers := make(map[string][]byte, 1)

	// Adding CorrelationId header
	if msg.Headers.CorrelationId != nil {
		headers["correlationId"] = []byte(*msg.Headers.CorrelationId)
	}

	return extensions.BrokerMessage{
		Headers: headers,
		Payload: payload,
	}, nil
}

// CorrelationID will give the correlation ID of the message, based on AsyncAPI spec
func (msg PongMessage) CorrelationID() string {
	if msg.Headers.CorrelationId != nil {
		return *msg.Headers.CorrelationId
	}

	return ""
}

// SetCorrelationID will set the correlation ID of the message, based on AsyncAPI spec
func (msg *PongMessage) SetCorrelationID(id string) {
	msg.Headers.CorrelationId = &id
}

// SetAsResponseFrom will correlate the message with the one passed in parameter.
// It will assign the 'req' message correlation ID to the message correlation ID,
// both specified in AsyncAPI spec.
func (msg *PongMessage) SetAsResponseFrom(req MessageWithCorrelationID) {
	id := req.CorrelationID()
	msg.Headers.CorrelationId = &id
}

const (
	// PingChannelPath is the constant representing the 'PingChannel' channel path.
	PingChannelPath = "ping.v3"
	// PongChannelPath is the constant representing the 'PongChannel' channel path.
	PongChannelPath = "pong.v3"
)

// ChannelsPaths is an array of all channels paths
var ChannelsPaths = []string{
	PingChannelPath,
	PongChannelPath,
}
