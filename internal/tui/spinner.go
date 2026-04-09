package tui

import (
	"math/rand/v2"
	"sync"
	"time"
)

// spinnerVerbs is the list of fun verbs shown while waiting for a response.
var spinnerVerbs = []string{
	"Accomplishing", "Actioning", "Actualizing", "Architecting",
	"Baking", "Beaming", "Beboppin'", "Befuddling",
	"Billowing", "Blanching", "Bloviating", "Boogieing",
	"Boondoggling", "Booping", "Bootstrapping", "Brewing",
	"Bunning", "Burrowing", "Calculating", "Canoodling",
	"Caramelizing", "Cascading", "Catapulting", "Cerebrating",
	"Channeling", "Choreographing", "Churning",
	"Coalescing", "Cogitating", "Combobulating",
	"Composing", "Computing", "Concocting", "Considering",
	"Contemplating", "Cooking", "Crafting", "Creating",
	"Crunching", "Crystallizing", "Cultivating", "Deciphering",
	"Deliberating", "Determining", "Dilly-dallying", "Discombobulating",
	"Doodling", "Drizzling", "Ebbing",
	"Effecting", "Elucidating", "Embellishing", "Enchanting",
	"Envisioning", "Evaporating", "Fermenting", "Fiddle-faddling",
	"Finagling", "Flowing",
	"Flummoxing", "Fluttering", "Forging", "Forming",
	"Frolicking", "Frosting", "Gallivanting", "Galloping",
	"Garnishing", "Generating", "Gesticulating", "Germinating",
	"Grooving", "Gusting", "Harmonizing",
	"Hashing", "Hatching", "Herding", "Honking",
	"Hullaballooing", "Hyperspacing", "Ideating", "Imagining",
	"Improvising", "Incubating", "Inferring", "Infusing",
	"Ionizing", "Jitterbugging", "Julienning", "Kneading",
	"Leavening", "Levitating", "Lollygagging", "Manifesting",
	"Marinating", "Meandering", "Metamorphosing", "Misting",
	"Moonwalking", "Moseying", "Mulling", "Mustering",
	"Musing", "Nebulizing", "Nesting",
	"Noodling", "Nucleating", "Orbiting", "Orchestrating",
	"Osmosing", "Perambulating", "Percolating", "Perusing",
	"Philosophizing", "Photosynthesizing", "Pollinating", "Pondering",
	"Pontificating", "Pouncing", "Precipitating", "Prestidigitating",
	"Processing", "Proofing", "Propagating", "Puttering",
	"Puzzling", "Quantumizing", "Razzle-dazzling", "Razzmatazzing",
	"Recombobulating", "Reticulating", "Roosting", "Ruminating",
	"Scampering", "Schlepping", "Scurrying",
	"Seasoning", "Shenaniganing", "Shimmying", "Simmering",
	"Skedaddling", "Sketching", "Slithering", "Smooshing",
	"Sock-hopping", "Spelunking", "Spinning", "Sprouting",
	"Stewing", "Sublimating", "Swirling", "Swooping",
	"Symbioting", "Synthesizing", "Tempering", "Thinking",
	"Thundering", "Tinkering", "Tomfoolering", "Topsy-turvying",
	"Transfiguring", "Transmuting", "Twisting", "Undulating",
	"Unfurling", "Unraveling", "Vibing", "Waddling",
	"Wandering", "Warping", "Whatchamacalliting", "Whirlpooling",
	"Whirring", "Whisking", "Wibbling", "Working",
	"Wrangling", "Zesting", "Zigzagging",
}

// spinnerSymbols are the rotating symbols shown before the verb.
var spinnerSymbols = []rune{'*', '+', '·'}

// spinnerState holds the current spinner verb and rotation timing.
type spinnerState struct {
	mu       sync.Mutex
	current  string
	updated  time.Time
	symIndex int
	turns    int // counts full symbol rotations for the current word
	nowFn    func() time.Time
}

var spinner = &spinnerState{}

func (s *spinnerState) now() time.Time {
	if s.nowFn != nil {
		return s.nowFn()
	}
	return time.Now()
}

// tick advances the spinner state and returns the formatted string.
func (s *spinnerState) tick() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()

	if s.current == "" {
		s.current = spinnerVerbs[rand.IntN(len(spinnerVerbs))]
		s.updated = now
	}

	// Advance symbol every 150ms
	if now.Sub(s.updated) >= 150*time.Millisecond {
		s.symIndex++
		if s.symIndex >= len(spinnerSymbols) {
			s.symIndex = 0
			s.turns++
		}
		// After 3 full rotations, pick a new word
		if s.turns >= 7 {
			s.current = spinnerVerbs[rand.IntN(len(spinnerVerbs))]
			s.turns = 0
			s.symIndex = 0
		}
		s.updated = now
	}

	sym := string(spinnerSymbols[s.symIndex])
	return sym + " " + s.current + "..."
}

// spinnerVerb returns the current spinner verb with a rotating symbol prefix.
func spinnerVerb() string {
	return spinner.tick()
}
