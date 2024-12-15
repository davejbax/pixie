package bootloader

type Grub struct {
	root string
}

func NewGrub(root string) *Grub {
	return &Grub{
		root: root,
	}
}
