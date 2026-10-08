package plex

type container struct {
	MediaContainer struct {
		TotalSize         int         `json:"totalSize"`
		MachineIdentifier string      `json:"machineIdentifier"`
		Directory         []directory `json:"Directory"`
		Hub               []hub       `json:"Hub"`
		Metadata          []metadata  `json:"Metadata"`
	} `json:"MediaContainer"`
}

type directory struct {
	Key   string `json:"key"`
	Type  string `json:"type"`
	Title string `json:"title"`
	Size  int    `json:"size"`
}

type hub struct {
	Type     string     `json:"type"`
	Metadata []metadata `json:"Metadata"`
}

type metadata struct {
	RatingKey        string  `json:"ratingKey"`
	PlaylistItemID   int64   `json:"playlistItemID"`
	Title            string  `json:"title"`
	Index            int     `json:"index"`
	ParentTitle      string  `json:"parentTitle"`
	ParentRatingKey  string  `json:"parentRatingKey"`
	GrandparentTitle string  `json:"grandparentTitle"`
	Year             int     `json:"year"`
	LeafCount        int     `json:"leafCount"`
	AddedAt          int64   `json:"addedAt"`
	Duration         int64   `json:"duration"` // ms
	Media            []media `json:"Media"`
}

type media struct {
	AudioCodec string `json:"audioCodec"`
	Container  string `json:"container"`
	Bitrate    int    `json:"bitrate"`
	Part       []part `json:"Part"`
}

type part struct {
	Key    string   `json:"key"`
	Stream []stream `json:"Stream"`
}

type stream struct {
	StreamType   int `json:"streamType"`
	SamplingRate int `json:"samplingRate"`
	BitDepth     int `json:"bitDepth"`
}
