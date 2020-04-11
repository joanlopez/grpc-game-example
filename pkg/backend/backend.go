package backend

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Game is the backend engine for the game. It can be used regardless of how
// game data is rendered, or if a game server is being used.
type Game struct {
	Entities        map[uuid.UUID]Identifier
	Mu              sync.RWMutex
	ChangeChannel   chan Change
	ActionChannel   chan Action
	LastAction      map[string]time.Time
	Score           map[uuid.UUID]int
	IsAuthoritative bool
}

// NewGame constructs a new Game struct.
func NewGame() *Game {
	game := Game{
		Entities:        make(map[uuid.UUID]Identifier),
		ActionChannel:   make(chan Action, 1),
		LastAction:      make(map[string]time.Time),
		ChangeChannel:   make(chan Change, 1),
		IsAuthoritative: true,
		Score:           make(map[uuid.UUID]int),
	}
	return &game
}

// Start begins the main game loop, which waits for new actions and updates the
// game state occordinly.
func (game *Game) Start() {
	// Read actions from the channel.
	go func() {
		for {
			action := <-game.ActionChannel
			action.Perform(game)
		}
	}()
	// Respond to laser collisions.
	if !game.IsAuthoritative {
		return
	}
	go func() {
		for {
			collisionMap := make(map[Coordinate][]Positioner)
			game.Mu.RLock()
			for _, entity := range game.Entities {
				positioner, ok := entity.(Positioner)
				if !ok {
					continue
				}
				position := positioner.Position()
				collisionMap[position] = append(collisionMap[position], positioner)
			}
			game.Mu.RUnlock()
			for _, entities := range collisionMap {
				if len(entities) <= 1 {
					continue
				}
				hasLaser := false
				var laserOwnerID uuid.UUID
				for _, entity := range entities {
					laser, ok := entity.(*Laser)
					if ok {
						hasLaser = true
						laserOwnerID = laser.OwnerID
						break
					}
				}
				if hasLaser {
					for _, entity := range entities {
						switch entity.(type) {
						case *Laser:
							laser := entity.(*Laser)
							change := RemoveEntityChange{
								Entity: laser,
							}
							select {
							case game.ChangeChannel <- change:
								// no-op.
							default:
								// no-op.
							}
							game.RemoveEntity(laser.ID())
						case *Player:
							player := entity.(*Player)
							game.Move(player.ID(), Coordinate{X: 0, Y: 0})
							change := PlayerRespawnChange{
								Player: player,
							}
							select {
							case game.ChangeChannel <- change:
								// no-op.
							default:
								// no-op.
							}
							// Change score.
							if player.ID() != laserOwnerID {
								game.Mu.Lock()
								game.Score[laserOwnerID]++
								game.Mu.Unlock()
							}
						}
					}
				}
			}
			time.Sleep(time.Millisecond * 20)
		}
	}()
}

func (game *Game) AddEntity(entity Identifier) {
	game.Mu.Lock()
	game.Entities[entity.ID()] = entity
	game.Mu.Unlock()
}

func (game *Game) UpdateEntity(entity Identifier) {
	// @todo is replacing the struct bad?
	game.Mu.Lock()
	game.Entities[entity.ID()] = entity
	game.Mu.Unlock()
}

func (game *Game) GetEntity(id uuid.UUID) Identifier {
	game.Mu.RLock()
	defer game.Mu.RUnlock()
	return game.Entities[id]
}

func (game *Game) RemoveEntity(id uuid.UUID) {
	game.Mu.Lock()
	delete(game.Entities, id)
	game.Mu.Unlock()
}

func (game *Game) Move(id uuid.UUID, position Coordinate) {
	game.Mu.Lock()
	game.Entities[id].(Mover).Move(position)
	game.Mu.Unlock()
}

func (game *Game) CheckLastActionTime(actionKey string, throttle int) bool {
	game.Mu.RLock()
	lastAction, ok := game.LastAction[actionKey]
	game.Mu.RUnlock()
	if ok && lastAction.After(time.Now().Add(-1*time.Duration(throttle)*time.Millisecond)) {
		return false
	}
	return true
}

func (game *Game) UpdateLastActionTime(actionKey string) {
	game.Mu.Lock()
	game.LastAction[actionKey] = time.Now()
	game.Mu.Unlock()
}

// Coordinate is used for all position-related variables.
type Coordinate struct {
	X int
	Y int
}

// Direction is used to represent Direction constants.
type Direction int

// Contains direction constants - DirectionStop will take no effect.
const (
	DirectionUp Direction = iota
	DirectionDown
	DirectionLeft
	DirectionRight
	DirectionStop
)

type Identifier interface {
	ID() uuid.UUID
}

type Positioner interface {
	Position() Coordinate
}

type Mover interface {
	Move(Coordinate)
}

type IdentifierBase struct {
	UUID uuid.UUID
}

func (e IdentifierBase) ID() uuid.UUID {
	return e.UUID
}

// Change is sent by the game engine in response to Actions.
type Change interface{}

// MoveChange is sent when the game engine moves an entity.
type MoveChange struct {
	Change
	Entity    Identifier
	Direction Direction
	Position  Coordinate
}

type AddEntityChange struct {
	Change
	Entity Identifier
}

type RemoveEntityChange struct {
	Change
	Entity Identifier
}

type PlayerRespawnChange struct {
	Change
	Player *Player
}

// Action is sent by the client when attempting to change game state. The
// engine can choose to reject Actions if they are invalid or performed too
// frequently.
type Action interface {
	Perform(game *Game)
}

// MoveAction is sent when a user presses an arrow key.
type MoveAction struct {
	Direction Direction
	ID        uuid.UUID
}

// Perform contains backend logic required to move an entity.
func (action MoveAction) Perform(game *Game) {
	entity := game.GetEntity(action.ID)
	if entity == nil {
		return
	}
	actionKey := fmt.Sprintf("%T:%s", action, entity.ID().String())
	if !game.CheckLastActionTime(actionKey, 50) {
		return
	}
	position := entity.(Positioner).Position()
	// Move the entity.
	switch action.Direction {
	case DirectionUp:
		position.Y--
	case DirectionDown:
		position.Y++
	case DirectionLeft:
		position.X--
	case DirectionRight:
		position.X++
	}
	game.Move(entity.ID(), position)
	// Inform the client that the entity moved.
	change := MoveChange{
		Entity:    entity,
		Direction: action.Direction,
		Position:  position,
	}
	select {
	case game.ChangeChannel <- change:
		// no-op.
	default:
		// no-op.
	}
	game.UpdateLastActionTime(actionKey)
}
