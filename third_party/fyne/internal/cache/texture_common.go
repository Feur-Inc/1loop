package cache

import (
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/internal/async"
)

var (
	textTextures   async.Map[FontCacheEntry, *textureInfo]
	objectTextures async.Map[fyne.CanvasObject, *textureInfo]
	lastTextSweep  time.Time
)

// DeleteTexture deletes the texture from the cache map.
func DeleteTexture(obj fyne.CanvasObject) {
	objectTextures.Delete(obj)
}

// GetTextTexture gets cached texture for a text run.
func GetTextTexture(ent FontCacheEntry) (TextureType, bool) {
	texInfo, ok := textTextures.Load(ent)
	if texInfo == nil || !ok {
		return NoTexture, false
	}
	texInfo.setAlive()
	return texInfo.texture, true
}

// TextureCanvas is the canvas an object's texture was made for, if it has
// one (1loop: driver.Recoloured).
func TextureCanvas(obj fyne.CanvasObject) (fyne.Canvas, bool) {
	texInfo, ok := objectTextures.Load(obj)
	if texInfo == nil || !ok {
		return nil, false
	}
	return texInfo.canvas, true
}

// GetTexture gets cached texture.
func GetTexture(obj fyne.CanvasObject) (TextureType, bool) {
	texInfo, ok := objectTextures.Load(obj)
	if texInfo == nil || !ok {
		return NoTexture, false
	}
	texInfo.setAlive()
	return texInfo.texture, true
}

// RangeExpiredTexturesFor range over the expired textures for the specified canvas.
//
// Note: If this is used to free textures, then it should be called inside a current
// gl context to ensure textures are deleted from gl.
func RangeExpiredTexturesFor(canvas fyne.Canvas, f func(fyne.CanvasObject)) {
	now := timeNow()

	textTextures.Range(func(key FontCacheEntry, tinfo *textureInfo) bool {
		// Just free text directly when that string/style combo is done.
		if tinfo.isExpired(now) && tinfo.canvas == canvas {
			textTextures.Delete(key)
			tinfo.textFree()
		}
		return true
	})

	objectTextures.Range(func(obj fyne.CanvasObject, tinfo *textureInfo) bool {
		if tinfo.isExpired(now) && tinfo.canvas == canvas {
			f(obj)
		}
		return true
	})
}

// ExpireStaleTextTexturesFor frees text textures on canvas that have not been
// drawn for at least maxAge. Called only after a full-frame paint (when every
// visible text just refreshed its expiry), at most once every 5 s, so abandoned
// streaming line prefixes do not linger for the full ValidDuration (1loop #491).
func ExpireStaleTextTexturesFor(canvas fyne.Canvas, maxAge time.Duration) {
	now := timeNow()
	if now.Sub(lastTextSweep) < 5*time.Second {
		return
	}
	lastTextSweep = now
	cutoff := now.Add(ValidDuration - maxAge)
	textTextures.Range(func(key FontCacheEntry, tinfo *textureInfo) bool {
		if tinfo.canvas == canvas && tinfo.expires.Before(cutoff) {
			textTextures.Delete(key)
			tinfo.textFree()
		}
		return true
	})
}

// RangeTexturesFor range over the textures for the specified canvas.
// It will not return the texture for a `canvas.Text` as their render lifecycle is handled separately.
//
// Note: If this is used to free textures, then it should be called inside a current
// gl context to ensure textures are deleted from gl.
func RangeTexturesFor(canvas fyne.Canvas, f func(fyne.CanvasObject)) {
	// Do nothing for texture cache, it lives outside the scope of an object.
	objectTextures.Range(func(obj fyne.CanvasObject, tinfo *textureInfo) bool {
		if tinfo.canvas == canvas {
			f(obj)
		}
		return true
	})
}

// DeleteTextTexturesFor deletes all text textures for the given canvas.
func DeleteTextTexturesFor(canvas fyne.Canvas) {
	textTextures.Range(func(key FontCacheEntry, tinfo *textureInfo) bool {
		if tinfo.canvas == canvas {
			textTextures.Delete(key)
			tinfo.textFree()
		}
		return true
	})
}

// SetTextTexture sets cached texture for a text run.
func SetTextTexture(ent FontCacheEntry, texture TextureType, canvas fyne.Canvas, free func()) {
	tinfo := prepareTexture(texture, canvas, free)
	textTextures.Store(ent, tinfo)
}

// SetTexture sets cached texture.
func SetTexture(obj fyne.CanvasObject, texture TextureType, canvas fyne.Canvas) {
	tinfo := prepareTexture(texture, canvas, nil)
	objectTextures.Store(obj, tinfo)
}

func prepareTexture(texture TextureType, canvas fyne.Canvas, free func()) *textureInfo {
	tinfo := &textureInfo{texture: texture, textFree: free}
	tinfo.canvas = canvas
	tinfo.setAlive()
	return tinfo
}

// textureCacheBase defines base texture cache object.
type textureCacheBase struct {
	expiringCache
	canvas fyne.Canvas
}
