package domain

import "github.com/google/uuid"

// FederationFollowID identifies one federation_follows row.
type FederationFollowID uuid.UUID

func (id FederationFollowID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id FederationFollowID) String() string  { return uuid.UUID(id).String() }

// FederationFeedItemID identifies one federation_feed_items row.
type FederationFeedItemID uuid.UUID

func (id FederationFeedItemID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id FederationFeedItemID) String() string  { return uuid.UUID(id).String() }

// FederationDeliveryID identifies one federation_deliveries row.
type FederationDeliveryID uuid.UUID

func (id FederationDeliveryID) UUID() uuid.UUID { return uuid.UUID(id) }
func (id FederationDeliveryID) String() string  { return uuid.UUID(id).String() }
