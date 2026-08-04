package tui

import (
	"math/rand/v2"
	"strings"
	"sync"
	"time"
)

// spinnerVerbs is the list of fun verbs shown while waiting for a response.
var (
	spinnerVerbs = []string{
		"Accomplishing", "Actioning", "Actualizing", "Architecting",
		"Augmenting", "Avataring", "Baking", "Beaming",
		"Beboppin'", "Befuddling", "Billowing", "Bioforging",
		"Blanching", "Bloviating", "Boogieing", "Boondoggling",
		"Booping", "Bootstrapping", "Braindancing", "Breaching",
		"Brewing", "Bunning", "Burrowing", "Calculating",
		"Canoodling", "Caramelizing", "Cascading", "Catapulting",
		"Cerebrating", "Channeling", "Chipburning", "Choreographing",
		"Chroming", "Churning", "Ciphering", "Coalescing",
		"Cogitating", "Combobulating", "Compiling", "Composing",
		"Computing", "Concocting", "Considering", "Constructing",
		"Contemplating", "Cooking", "Coreslicing", "Cowboying",
		"Crafting", "Creating", "Crunching", "Crystallizing",
		"Cultivating", "Cyberdecking", "Darkpooling", "Datamining",
		"Datavaulting", "Deciphering", "Decompiling", "Decrypting",
		"Deliberating", "Deltasleeping", "Depixelating", "Dermatroding",
		"Determining", "Dilly-dallying", "Discombobulating", "Dissolving",
		"Doodling", "Downlinking", "Drifting", "Drizzling",
		"Ebbing", "Edgerunning", "Effecting", "Electrogliding",
		"Elucidating", "Embellishing", "Enchanting", "Encrypting",
		"Envisioning", "Evaporating", "Fermenting", "Fiddle-faddling",
		"Finagling", "Firewalling", "Flatcoding", "Flowing",
		"Flummoxing", "Fluttering", "Forging", "Forming",
		"Fragmenting", "Frolicking", "Frosting", "Gallivanting",
		"Galloping", "Gargoyleing", "Garnishing", "Generating",
		"Germinating", "Gesticulating", "Ghosting", "Glitching",
		"Gridwalking", "Grooving", "Gusting", "Hardwiring",
		"Harmonizing", "Hashing", "Hatching", "Herding",
		"Hexdumping", "Hologramming", "Honking", "Hotswapping",
		"Hullaballooing", "Hyperspacing", "Hyperthreading", "Ideating",
		"Imagining", "Improvising", "Incubating", "Inferring",
		"Infusing", "Interfacing", "Ionizing", "Iterating",
		"Jitterbugging", "Jockeying", "Julienning", "Kernelizing",
		"Kneading", "Leavening", "Levitating", "Linecooking",
		"Lollygagging", "Looping", "Manifesting", "Marinating",
		"Matrixing", "Meandering", "Megafluxing", "Meshing",
		"Metamorphosing", "Metaversing", "Mirrorshading", "Misting",
		"Moonwalking", "Morphing", "Moseying", "Mulling",
		"Mustering", "Musing", "Nanoweaving", "Nebulizing",
		"Neontracing", "Nesting", "Netrunning", "Neural-linking",
		"Neuromancing", "Noodling", "Nucleating", "Obfuscating",
		"Orbiting", "Orchestrating", "Osmosing", "Overclocking",
		"Overwatching", "Perambulating", "Percolating", "Perusing",
		"Philosophizing", "Photosynthesizing", "Pixeldrifting", "Pollinating",
		"Pondering", "Pontificating", "Pouncing", "Precipitating",
		"Prestidigitating", "Processing", "Proofing", "Propagating",
		"Puttering", "Puzzling", "Quantumizing", "Razzle-dazzling",
		"Razzmatazzing", "Recompiling", "Recombobulating", "Reflashing",
		"Reticulating", "Roosting", "Ruminating", "Sandboxing",
		"Scampering", "Schlepping", "Scurrying", "Seasoning",
		"Shadowcasting", "Shenaniganing", "Shimmying", "Simsense-loading",
		"Simstimming", "Simmering", "Skedaddling", "Sketching",
		"Slithering", "Smooshing", "Sock-hopping", "Soldering",
		"Spelunking", "Spinning", "Sprawling", "Sprouting",
		"Stewing", "Sublimating", "Subroutining", "Swirling",
		"Swooping", "Symbioting", "Synapse-firing", "Synthwaving",
		"Synthesizing", "Tempering", "Tessier-ashpooling", "Thinking",
		"Thundering", "Tinkering", "Tomfoolering", "Topsy-turvying",
		"Tracing", "Transfiguring", "Transmuting", "Trode-jockeying",
		"Tunneling", "Twisting", "Undulating", "Unfurling",
		"Unraveling", "Uplinking", "Vaporwaving", "Vibing",
		"Voodoo-boying", "Voxelizing", "Waddling", "Wandering",
		"Warping", "Wetware-syncing", "Whatchamacalliting", "Whirlpooling",
		"Whirring", "Whisking", "Wibbling", "Wintermuting",
		"Wireframing", "Working", "Wrangling", "Zesting",
		"Zigzagging", "Zone-tripping",
	}

	spinnerVerbWidth = maxStringWidth(spinnerVerbs)
	spinnerTextWidth = spinnerVerbWidth + len("* ") + len("...")
)

// spinnerSymbols are the rotating symbols shown before the verb.
var spinnerSymbols = []rune{'*', '+', '∙'}

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
	return sym + " " + s.current + "..." + spinnerVerbPadding(s.current)
}

func spinnerVerbPadding(verb string) string {
	if len(verb) >= spinnerVerbWidth {
		return ""
	}
	return strings.Repeat(" ", spinnerVerbWidth-len(verb))
}

func maxStringWidth(values []string) int {
	maxWidth := 0
	for _, value := range values {
		if len(value) > maxWidth {
			maxWidth = len(value)
		}
	}
	return maxWidth
}

// paddedStatusMode returns an idle mode label padded to the same width as spinnerVerb().
func paddedStatusMode(mode string) string {
	if len(mode) >= spinnerTextWidth {
		return mode
	}
	return mode + strings.Repeat(" ", spinnerTextWidth-len(mode))
}

// spinnerVerb returns the current spinner verb with a rotating symbol prefix.
func spinnerVerb() string {
	return spinner.tick()
}
