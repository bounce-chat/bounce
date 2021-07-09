package ui

type BounceUI interface {
	Build(string)
	Run()
	Quit()
}
