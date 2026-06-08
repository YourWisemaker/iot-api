package store

import (
	"errors"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/YourWisemaker/iot-api/internal/models"
)

// userDoc is the BSON representation of a user.
type userDoc struct {
	ID           string    `bson:"id"`
	Username     string    `bson:"username"`
	PasswordHash string    `bson:"password_hash"`
	Roles        []string  `bson:"roles"`
	CreatedAt    time.Time `bson:"created_at"`
}

func userToDoc(u models.User) userDoc {
	return userDoc{
		ID:           u.ID,
		Username:     u.Username,
		PasswordHash: u.PasswordHash,
		Roles:        u.Roles,
		CreatedAt:    u.CreatedAt,
	}
}

func docToUser(d userDoc) models.User {
	return models.User{
		ID:           d.ID,
		Username:     d.Username,
		PasswordHash: d.PasswordHash,
		Roles:        d.Roles,
		CreatedAt:    d.CreatedAt,
	}
}

// refreshDoc is the BSON representation of a refresh token.
type refreshDoc struct {
	TokenHash string    `bson:"token_hash"`
	UserID    string    `bson:"user_id"`
	ExpiresAt time.Time `bson:"expires_at"`
	CreatedAt time.Time `bson:"created_at"`
}

// CreateUser inserts a new user, enforcing a unique username.
func (s *MongoStore) CreateUser(u models.User) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.users.InsertOne(ctx, userToDoc(u))
	if mongo.IsDuplicateKeyError(err) {
		return ErrDuplicateUser
	}
	return err
}

// GetUserByUsername looks up a user by username.
func (s *MongoStore) GetUserByUsername(username string) (models.User, error) {
	return s.findUser(bson.M{"username": username})
}

// GetUserByID looks up a user by ID.
func (s *MongoStore) GetUserByID(id string) (models.User, error) {
	return s.findUser(bson.M{"id": id})
}

func (s *MongoStore) findUser(filter bson.M) (models.User, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc userDoc
	err := s.users.FindOne(ctx, filter).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.User{}, ErrNotFound
	}
	if err != nil {
		return models.User{}, err
	}
	return docToUser(doc), nil
}

// ListUsers returns all users sorted by creation time.
func (s *MongoStore) ListUsers() []models.User {
	ctx, cancel := s.ctx()
	defer cancel()
	cur, err := s.users.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}))
	if err != nil {
		return nil
	}
	defer cur.Close(ctx)

	var out []models.User
	for cur.Next(ctx) {
		var doc userDoc
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		out = append(out, docToUser(doc))
	}
	return out
}

// UpdateUser replaces a user's mutable fields (roles, password hash).
func (s *MongoStore) UpdateUser(u models.User) error {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.users.UpdateOne(ctx, bson.M{"id": u.ID}, bson.M{"$set": bson.M{
		"roles":         u.Roles,
		"password_hash": u.PasswordHash,
	}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteUser removes a user and their refresh tokens.
func (s *MongoStore) DeleteUser(id string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	res, err := s.users.DeleteOne(ctx, bson.M{"id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	_, _ = s.refresh.DeleteMany(ctx, bson.M{"user_id": id})
	return nil
}

// StoreRefreshToken persists a hashed refresh token.
func (s *MongoStore) StoreRefreshToken(rt models.RefreshToken) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.refresh.InsertOne(ctx, refreshDoc{
		TokenHash: rt.TokenHash,
		UserID:    rt.UserID,
		ExpiresAt: rt.ExpiresAt,
		CreatedAt: rt.CreatedAt,
	})
	return err
}

// GetRefreshToken returns a refresh token by hash, treating expired tokens as
// not found.
func (s *MongoStore) GetRefreshToken(tokenHash string) (models.RefreshToken, error) {
	ctx, cancel := s.ctx()
	defer cancel()
	var doc refreshDoc
	err := s.refresh.FindOne(ctx, bson.M{"token_hash": tokenHash}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return models.RefreshToken{}, ErrNotFound
	}
	if err != nil {
		return models.RefreshToken{}, err
	}
	if !doc.ExpiresAt.IsZero() && time.Now().After(doc.ExpiresAt) {
		return models.RefreshToken{}, ErrNotFound
	}
	return models.RefreshToken{
		TokenHash: doc.TokenHash,
		UserID:    doc.UserID,
		ExpiresAt: doc.ExpiresAt,
		CreatedAt: doc.CreatedAt,
	}, nil
}

// DeleteRefreshToken removes a single refresh token.
func (s *MongoStore) DeleteRefreshToken(tokenHash string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.refresh.DeleteOne(ctx, bson.M{"token_hash": tokenHash})
	return err
}

// DeleteUserRefreshTokens removes all refresh tokens for a user.
func (s *MongoStore) DeleteUserRefreshTokens(userID string) error {
	ctx, cancel := s.ctx()
	defer cancel()
	_, err := s.refresh.DeleteMany(ctx, bson.M{"user_id": userID})
	return err
}
