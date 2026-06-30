package captcha

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"strings"

	"github.com/mojocn/base64Captcha"
)

// 去掉易混淆字符：0/O/1/I/L
const source = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// ciStore 包装默认内存 store，做大小写不敏感校验
type ciStore struct{ inner base64Captcha.Store }

func (s ciStore) Set(id, value string) error {
	return s.inner.Set(id, strings.ToLower(value))
}
func (s ciStore) Get(id string, clear bool) string { return s.inner.Get(id, clear) }
func (s ciStore) Verify(id, code string, clear bool) bool {
	return s.inner.Verify(id, strings.ToLower(code), clear)
}

var (
	// 纯黑背景，后处理时把黑色像素转成透明
	bgColor = &color.RGBA{R: 0, G: 0, B: 0, A: 0xff}
	driver  = base64Captcha.NewDriverString(
		60, 180, 0,
		0,
		4, source, bgColor, nil, nil,
	)
	store = ciStore{base64Captcha.DefaultMemStore}
)

const dataPrefix = "data:image/png;base64,"

// Generate 生成验证码：返回 id + 透明背景 PNG 的 base64 data URI
func Generate() (id, b64 string, err error) {
	id, raw, _, err := base64Captcha.NewCaptcha(driver, store).Generate()
	if err != nil || !strings.HasPrefix(raw, dataPrefix) {
		return id, raw, err
	}
	pngBytes, err := base64.StdEncoding.DecodeString(raw[len(dataPrefix):])
	if err != nil {
		return id, raw, nil
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return id, raw, nil
	}

	// 接近黑色 → 透明（背景）；其它像素 → 实心字符，统一染成主题浅蓝
	bounds := img.Bounds()
	out := image.NewRGBA(bounds)
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := img.At(x, y).RGBA()
			r8, g8, b8 := uint8(r>>8), uint8(g>>8), uint8(b>>8)
			mx := r8
			if g8 > mx {
				mx = g8
			}
			if b8 > mx {
				mx = b8
			}
			if mx < 32 {
				out.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
				continue
			}
			// 实心字符：#7DD3FC（亮蓝）配深色主题
			out.SetRGBA(x, y, color.RGBA{R: 0x7d, G: 0xd3, B: 0xfc, A: 0xff})
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, out); err != nil {
		return id, raw, nil
	}
	return id, dataPrefix + base64.StdEncoding.EncodeToString(buf.Bytes()), nil
}

// Verify 校验，不区分大小写，校验后立即失效（一次性）
func Verify(id, code string) bool {
	if id == "" || code == "" {
		return false
	}
	return store.Verify(id, code, true)
}