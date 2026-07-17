package connect

import (
	"context"
	"errors"
	"fmt"

	connectrpc "connectrpc.com/connect"
	"github.com/google/uuid"

	v1 "github.com/johnnycube/cairn-core/gen/proto/cairn/v1"
	"github.com/johnnycube/cairn-core/gen/proto/cairn/v1/cairnv1connect"
	"github.com/johnnycube/cairn-core/internal/domain"
	"github.com/johnnycube/cairn-core/internal/port"
)

// NotificationServer implements cairnv1connect.NotificationServiceHandler.
// Only ListNotifications is implemented; the rest fall back to the embedded
// Unimplemented base (mark-read, count-unread, preferences, …) and return
// CodeUnimplemented until their use-cases land.
type NotificationServer struct {
	cairnv1connect.UnimplementedNotificationServiceHandler

	Notifications port.NotificationRepo
}

var _ cairnv1connect.NotificationServiceHandler = (*NotificationServer)(nil)

func NewNotificationServer(notifications port.NotificationRepo) *NotificationServer {
	return &NotificationServer{Notifications: notifications}
}

// ListNotifications returns the user's in-app feed, newest first.
//
// Pagination is offset/limit — the proto uses an opaque cursor, but for
// the v1 in-app feed offset is sufficient (the repo returns rows in a
// stable created_at DESC order). The cursor is just a base-10 offset
// encoded as a string. Empty cursor = page 0.
func (s *NotificationServer) ListNotifications(
	ctx context.Context,
	req *connectrpc.Request[v1.ListNotificationsRequest],
) (*connectrpc.Response[v1.ListNotificationsResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	limit, offset := pageFrom(req.Msg.GetPage())

	notifs, err := s.Notifications.ListNotificationsForUser(
		ctx, userID, req.Msg.GetOnlyUnread(), limit, offset,
	)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("list notifications: %w", err))
	}

	out := &v1.ListNotificationsResponse{
		Notifications: make([]*v1.NotificationEvent, 0, len(notifs)),
	}
	for _, n := range notifs {
		out.Notifications = append(out.Notifications, notificationToProto(n))
	}

	// Next-cursor is the next offset when we filled a full page.
	if len(notifs) == limit {
		out.Page = &v1.PageResponse{
			NextCursor: encodeOffset(offset + limit),
			Total:      -1,
		}
	} else {
		out.Page = &v1.PageResponse{NextCursor: "", Total: int64(offset + len(notifs))}
	}
	return connectrpc.NewResponse(out), nil
}

// MarkNotificationRead flips one notification to read = true and returns
// the updated row. The repo's GetByID enforces user-ownership; cross-user
// IDs surface as NotFound, never as PermissionDenied (we don't confirm
// "exists but isn't yours").
func (s *NotificationServer) MarkNotificationRead(
	ctx context.Context,
	req *connectrpc.Request[v1.MarkNotificationReadRequest],
) (*connectrpc.Response[v1.MarkNotificationReadResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}

	id, err := uuid.Parse(req.Msg.GetId())
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInvalidArgument,
			errors.New("invalid notification id"))
	}
	nid := domain.NotificationID(id)

	if err := s.Notifications.MarkRead(ctx, userID, []domain.NotificationID{nid}); err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("mark notification read: %w", err))
	}

	updated, err := s.Notifications.GetByID(ctx, userID, nid)
	if err != nil {
		return nil, notFoundErr(err)
	}

	return connectrpc.NewResponse(&v1.MarkNotificationReadResponse{
		Notification: notificationToProto(updated),
	}), nil
}

// MarkAllNotificationsRead flips every unread row of the caller to
// read = true. Ignores the request's `types` filter for now — when the
// repo gains a per-type variant we'll pass it through.
func (s *NotificationServer) MarkAllNotificationsRead(
	ctx context.Context,
	req *connectrpc.Request[v1.MarkAllNotificationsReadRequest],
) (*connectrpc.Response[v1.MarkAllNotificationsReadResponse], error) {
	userID, err := userIDFromHandlerCtx(ctx, req)
	if err != nil {
		return nil, err
	}
	n, err := s.Notifications.MarkAllReadForUser(ctx, userID)
	if err != nil {
		return nil, connectrpc.NewError(connectrpc.CodeInternal,
			fmt.Errorf("mark all notifications read: %w", err))
	}
	return connectrpc.NewResponse(&v1.MarkAllNotificationsReadResponse{
		MarkedCount: int32(n),
	}), nil
}
