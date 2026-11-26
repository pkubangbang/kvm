package mouse

// The button byte every inking report carries: the LEFT button. Kept as the
// package default (Rel.btn starts here); a caller that wants a right or
// middle drag calls Rel.SetButton(BtnRight|BtnMiddle).
const btnLeft = 1
