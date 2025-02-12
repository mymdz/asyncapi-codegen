// Package "issue181" provides primitives to interact with the AsyncAPI specification.
//
// Code generated by github.com/lerenn/asyncapi-codegen version (devel) DO NOT EDIT.
package issue181

import (
	"context"
	"fmt"

	"github.com/lerenn/asyncapi-codegen/pkg/extensions"
)

// AppSubscriber contains all handlers that are listening messages for App
type AppSubscriber interface {
	// GetServiceInfoOperationReceived receive all Request messages from Request channel.
	GetServiceInfoOperationReceived(ctx context.Context, msg RequestMessage) error
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
	ctx = context.WithValue(ctx, extensions.ContextKeyIsVersion, "")
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

	if err := c.SubscribeToGetServiceInfoOperation(ctx, as.GetServiceInfoOperationReceived); err != nil {
		return err
	}

	return nil
}

// UnsubscribeFromAllChannels will stop the subscription of all remaining subscribed channels
func (c *AppController) UnsubscribeFromAllChannels(ctx context.Context) {
	c.UnsubscribeFromGetServiceInfoOperation(ctx)
}

// SubscribeToGetServiceInfoOperation will receive Request messages from Request channel.
//
// Callback function 'fn' will be called each time a new message is received.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
// If you need support for other messages, please raise an issue.
func (c *AppController) SubscribeToGetServiceInfoOperation(
	ctx context.Context,
	fn func(ctx context.Context, msg RequestMessage) error,
) error {
	// Get channel address
	addr := "v3.issue181.reception"

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
				msg, err := brokerMessageToRequestMessage(acknowledgeableBrokerMessage.BrokerMessage)
				if err != nil {
					return err
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

// ReplyToGetServiceInfoOperation is a helper function to
// reply to a Request message with a ReplyMessageFromReplyChannel message on Reply channel.
func (c *AppController) ReplyToGetServiceInfoOperation(ctx context.Context, recvMsg RequestMessage, fn func(replyMsg *ReplyMessageFromReplyChannel)) error {
	// Create reply message
	replyMsg := NewReplyMessageFromReplyChannel()

	// Execute callback function
	fn(&replyMsg)

	// Publish reply
	chanAddr := recvMsg.Headers.ReplyTo

	return c.SendAsReplyToGetServiceInfoOperation(ctx, chanAddr, replyMsg)
}

// UnsubscribeFromGetServiceInfoOperation will stop the reception of Request messages from Request channel.
// A timeout can be set in context to avoid blocking operation, if needed.
func (c *AppController) UnsubscribeFromGetServiceInfoOperation(
	ctx context.Context,
) {
	// Get channel address
	addr := "v3.issue181.reception"

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

// SendAsReplyToGetServiceInfoOperation will send a ReplyMessageFromReplyChannel message on Reply channel.
//
// NOTE: for now, this only support the first message from AsyncAPI list.
// If you need support for other messages, please raise an issue.
func (c *AppController) SendAsReplyToGetServiceInfoOperation(
	ctx context.Context,
	chanAddr string,
	msg ReplyMessageFromReplyChannel,
) error {
	// Set channel address
	addr := chanAddr

	// Set context
	ctx = addAppContextValues(ctx, addr)
	ctx = context.WithValue(ctx, extensions.ContextKeyIsDirection, "publication")

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

// UserController is the structure that provides sending capabilities to the
// developer and and connect the broker with the User
type UserController struct {
	controller
}

// NewUserController links the User to the broker
func NewUserController(bc extensions.BrokerController, options ...ControllerOption) (*UserController, error) {
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

	return &UserController{controller: controller}, nil
}

func (c UserController) wrapMiddlewares(
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

func (c UserController) executeMiddlewares(ctx context.Context, msg *extensions.BrokerMessage, callback extensions.NextMiddleware) error {
	// Wrap middleware to have 'next' function when calling them
	wrapped := c.wrapMiddlewares(c.middlewares, callback)

	// Execute wrapped middlewares
	return wrapped(ctx, msg)
}

func addUserContextValues(ctx context.Context, addr string) context.Context {
	ctx = context.WithValue(ctx, extensions.ContextKeyIsVersion, "")
	ctx = context.WithValue(ctx, extensions.ContextKeyIsProvider, "user")
	return context.WithValue(ctx, extensions.ContextKeyIsChannel, addr)
}

// Close will clean up any existing resources on the controller
func (c *UserController) Close(ctx context.Context) {
	// Unsubscribing remaining channels
}

// SendToGetServiceInfoOperation will send a Request message on Request channel.
//
// NOTE: this won't wait for reply, use the normal version to get the reply or do the catching reply manually.
// NOTE: for now, this only support the first message from AsyncAPI list.
// If you need support for other messages, please raise an issue.
func (c *UserController) SendToGetServiceInfoOperation(
	ctx context.Context,
	msg RequestMessage,
) error {
	// Set channel address
	addr := "v3.issue181.reception"

	// Set context
	ctx = addUserContextValues(ctx, addr)
	ctx = context.WithValue(ctx, extensions.ContextKeyIsDirection, "publication")

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

// RequestToGetServiceInfoOperation will send a Request message on Request channel
// and wait for a ReplyMessageFromReplyChannel message from Reply channel.
//
// If a correlation ID is set in the AsyncAPI, then this will wait for the
// reply with the same correlation ID. Otherwise, it will returns the first
// message on the reply channel.
//
// A timeout can be set in context to avoid blocking operation, if needed.

func (c *UserController) RequestToGetServiceInfoOperation(
	ctx context.Context,
	msg RequestMessage,
) (ReplyMessageFromReplyChannel, error) {
	// Get receiving channel address
	addr := msg.Headers.ReplyTo

	// Set context
	ctx = addUserContextValues(ctx, addr)

	// Subscribe to broker channel
	sub, err := c.broker.Subscribe(ctx, addr)
	if err != nil {
		c.logger.Error(ctx, err.Error())
		return ReplyMessageFromReplyChannel{}, err
	}
	c.logger.Info(ctx, "Subscribed to channel")

	// Close receiver on leave
	defer func() {
		// Stop the subscription
		sub.Cancel(ctx)

		// Logging unsubscribing
		c.logger.Info(ctx, "Unsubscribed from channel")
	}()

	// Send the message
	if err := c.SendToGetServiceInfoOperation(ctx, msg); err != nil {
		c.logger.Error(ctx, "error happened when sending message", extensions.LogInfo{Key: "error", Value: err.Error()})
		return ReplyMessageFromReplyChannel{}, fmt.Errorf("error happened when sending message: %w", err)
	}

	// Wait for corresponding response
	for {
		select {
		case acknowledgeableBrokerMessage, open := <-sub.MessagesChannel():
			// If subscription is closed and there is no more message
			// (i.e. uninitialized message), then the subscription ended before
			// receiving the expected message
			if !open && acknowledgeableBrokerMessage.IsUninitialized() {
				c.logger.Error(ctx, "Channel closed before getting message")
				return ReplyMessageFromReplyChannel{}, extensions.ErrSubscriptionCanceled
			}

			// There is correlation no ID, so it will automatically return at
			// the first received message.

			// Set context with received values as it is the expected message
			msgCtx := context.WithValue(ctx, extensions.ContextKeyIsBrokerMessage, acknowledgeableBrokerMessage.String())
			msgCtx = context.WithValue(msgCtx, extensions.ContextKeyIsDirection, "reception")

			// Execute middlewares before returning
			if err := c.executeMiddlewares(msgCtx, &acknowledgeableBrokerMessage.BrokerMessage, nil); err != nil {
				return ReplyMessageFromReplyChannel{}, err
			}

			// Return the message to the caller
			//
			// NOTE: it is transformed from the broker again, as it could have
			// been modified by middlewares
			return brokerMessageToReplyMessageFromReplyChannel(acknowledgeableBrokerMessage.BrokerMessage)
		case <-ctx.Done(): // Set corrsponding error if context is done
			c.logger.Error(ctx, "Context done before getting message")
			return ReplyMessageFromReplyChannel{}, extensions.ErrContextCanceled
		}
	}
}

// AsyncAPIVersion is the version of the used AsyncAPI document
const AsyncAPIVersion = ""

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

// ReplyMessageFromReplyChannel is the message expected for 'ReplyMessageFromReplyChannel' channel.
type ReplyMessageFromReplyChannel struct {
	// Payload will be inserted in the message payload
	Payload string
}

func NewReplyMessageFromReplyChannel() ReplyMessageFromReplyChannel {
	var msg ReplyMessageFromReplyChannel

	return msg
}

// brokerMessageToReplyMessageFromReplyChannel will fill a new ReplyMessageFromReplyChannel with data from generic broker message
func brokerMessageToReplyMessageFromReplyChannel(bMsg extensions.BrokerMessage) (ReplyMessageFromReplyChannel, error) {
	var msg ReplyMessageFromReplyChannel

	// Convert to string
	payload := string(bMsg.Payload)
	msg.Payload = payload // No need for type conversion to reference

	// TODO: run checks on msg type

	return msg, nil
}

// toBrokerMessage will generate a generic broker message from ReplyMessageFromReplyChannel data
func (msg ReplyMessageFromReplyChannel) toBrokerMessage() (extensions.BrokerMessage, error) {
	// TODO: implement checks on message

	// Convert to []byte
	payload := []byte(msg.Payload)

	// There is no headers here
	headers := make(map[string][]byte, 0)

	return extensions.BrokerMessage{
		Headers: headers,
		Payload: payload,
	}, nil
}

// Message 'RequestMessageFromRequestChannel' reference another one at '#/components/messages/request'.
// This should be fixed in a future version to allow message override.
// If you encounter this message, feel free to open an issue on this subject
// to let know that you need this functionnality.

// HeadersFromRequestMessage is a schema from the AsyncAPI specification required in messages
type HeadersFromRequestMessage struct {
	// Description: Channel used to respond to request
	ReplyTo string `json:"replyTo"`
}

// RequestMessage is the message expected for 'RequestMessage' channel.
type RequestMessage struct {
	// Headers will be used to fill the message headers
	Headers HeadersFromRequestMessage

	// Payload will be inserted in the message payload
	Payload string
}

func NewRequestMessage() RequestMessage {
	var msg RequestMessage

	return msg
}

// brokerMessageToRequestMessage will fill a new RequestMessage with data from generic broker message
func brokerMessageToRequestMessage(bMsg extensions.BrokerMessage) (RequestMessage, error) {
	var msg RequestMessage

	// Convert to string
	payload := string(bMsg.Payload)
	msg.Payload = payload // No need for type conversion to reference

	// Get each headers from broker message
	for k, v := range bMsg.Headers {
		switch {
		case k == "replyTo": // Retrieving ReplyTo header
			msg.Headers.ReplyTo = string(v)
		default:
			// TODO: log unknown error
		}
	}

	// TODO: run checks on msg type

	return msg, nil
}

// toBrokerMessage will generate a generic broker message from RequestMessage data
func (msg RequestMessage) toBrokerMessage() (extensions.BrokerMessage, error) {
	// TODO: implement checks on message

	// Convert to []byte
	payload := []byte(msg.Payload)

	// Add each headers to broker message
	headers := make(map[string][]byte, 1)

	// Adding ReplyTo header
	headers["replyTo"] = []byte(msg.Headers.ReplyTo)

	return extensions.BrokerMessage{
		Headers: headers,
		Payload: payload,
	}, nil
}

const (
	// ReplyChannelPath is the constant representing the 'ReplyChannel' channel path.
	ReplyChannelPath = ""
	// RequestChannelPath is the constant representing the 'RequestChannel' channel path.
	RequestChannelPath = "v3.issue181.reception"
)

// ChannelsPaths is an array of all channels paths
var ChannelsPaths = []string{
	ReplyChannelPath,
	RequestChannelPath,
}
