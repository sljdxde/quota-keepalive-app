package gui

import (
	"strings"
	"testing"
)

// 主窗口宠物与菜单栏小窗共用同一套「美漫风」文字语言：气泡是白底粗描边，
// 捕获结果是斜切大字横幅，失败统一显示「捕获失败」（不再区分躲开/弹开）。
func TestComicPetTextOnMainWindowAndMiniWindow(t *testing.T) {
	mainData, err := frontendFS.ReadFile("frontend/main.html")
	if err != nil {
		t.Fatal(err)
	}
	mainPage := string(mainData)
	requireStrings(t, mainPage, "主窗口宠物美漫文字",
		`id="main-pet-popup"`,
		`class="main-pet-popup"`,
		`.main-pet-popup.miss`,
		`.main-pet-bubble.pop`,
		`main-pet-bubble-speak`,
		`function showMainPetPopup(`,
		`function speakMainPet(`,
		`function scheduleMainPetTalk(`,
		`function stopMainPetTalk(`,
		`showMainPetPopup("捕获成功！", "ok")`,
		`showMainPetPopup("捕获失败", "miss")`,
		`border: 2px solid var(--fg);`,
		`aimForClientPoint(clientX, clientY, stageRect, viewerRect)`,
		`document.addEventListener("pointermove"`,
		`window.addEventListener("pointerup"`,
		`if (!petPressActive) return;`,
	)
	if strings.Contains(mainPage, `result.dodged
            ? "它躲开了！再来一球"`) {
		t.Log("失败提示仍保留原因文案（气泡），但横幅必须是捕获失败")
	}

	miniData, err := frontendFS.ReadFile("frontend/petmini.html")
	if err != nil {
		t.Fatal(err)
	}
	miniPage := string(miniData)
	requireStrings(t, miniPage, "迷你宠物美漫文字",
		`id="pm-bubble"`,
		`.pm-bubble.pop`,
		`pm-bubble-speak`,
		`function speak(`,
		`function scheduleTalk(`,
		`showPopup("捕获失败", "miss")`,
		`speak("点一下精灵球把它放出来")`,
		`scheduleTalk()`,
	)
}
