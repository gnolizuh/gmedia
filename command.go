package rtmp

type ConnectCommand struct {
	Trans   int
	Object struct {
		App      string
		SwfURL   string
		Type     string
		FlashVer string
		TcURL    string
	}
}

type ReleaseStreamCommand struct {
	Trans   int
	Name    string
}

type CreateStreamCommand struct {
	Trans   int
}

type CloseStreamCommand struct {
	Stream  float64
}

type DeleteStreamCommand struct {
	Stream  float64
}

type FCPublishCommand struct {
	Trans   int
	Name    string
}

type PublishCommand struct {
	Name    string
	Type    string
}

type PlayCommand struct {
	Name     string
	Start    float64
	Duration float64
	Reset    int
}

type Play2Command struct {
	Start      float64
	StreamName string
}

type SeekCommand struct {
	Offset  float64
}

type PauseCommand struct {
	Pause    bool
	Position float64
}
