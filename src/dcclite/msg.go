package main

import (
	"os"
	"strings"
)

// Native (Go) messages shown outside the web UI. Language: DCC_LANG, else the OS UI language.
var nativeMsgs = map[string]map[string]string{
	"en": {"webview2": "DCC Lite needs the Microsoft Edge WebView2 Runtime, which is not installed on this computer.\n\nInstall it from microsoft.com/edge/webview2, or use the DCCLite.exe version that opens in your browser.", "window": "The DCC Lite window could not be created.", "nodevice": "The device was not found.", "disabled": "This device is disabled.", "badaddr": "The channel address is not usable."},
	"ar": {"webview2": "يحتاج DCC Lite إلى بيئة Microsoft Edge WebView2 وهي غير مثبّتة على هذا الجهاز.\n\nثبّتها من microsoft.com/edge/webview2 أو استخدم نسخة DCCLite.exe التي تُفتح في المتصفح.", "window": "تعذّر إنشاء نافذة DCC Lite.", "nodevice": "لم يتم العثور على الجهاز.", "disabled": "هذا الجهاز معطّل.", "badaddr": "عنوان القناة غير صالح للاستخدام."},
	"fr": {"webview2": "DCC Lite nécessite Microsoft Edge WebView2 Runtime, qui n'est pas installé sur cet ordinateur.\n\nInstallez-le depuis microsoft.com/edge/webview2 ou utilisez la version DCCLite.exe qui s'ouvre dans le navigateur.", "window": "La fenêtre de DCC Lite n'a pas pu être créée.", "nodevice": "L'appareil est introuvable.", "disabled": "Cet appareil est désactivé.", "badaddr": "L'adresse de la chaîne est inutilisable."},
	"es": {"webview2": "DCC Lite necesita Microsoft Edge WebView2 Runtime, que no está instalado en este equipo.\n\nInstálelo desde microsoft.com/edge/webview2 o use la versión DCCLite.exe que se abre en el navegador.", "window": "No se pudo crear la ventana de DCC Lite.", "nodevice": "No se encontró el dispositivo.", "disabled": "Este dispositivo está desactivado.", "badaddr": "La dirección del canal no es utilizable."},
	"de": {"webview2": "DCC Lite benötigt die Microsoft Edge WebView2 Runtime, die auf diesem Computer nicht installiert ist.\n\nInstallieren Sie sie von microsoft.com/edge/webview2 oder verwenden Sie die Browser-Version DCCLite.exe.", "window": "Das DCC-Lite-Fenster konnte nicht erstellt werden.", "nodevice": "Das Gerät wurde nicht gefunden.", "disabled": "Dieses Gerät ist deaktiviert.", "badaddr": "Die Kanaladresse ist nicht verwendbar."},
	"tr": {"webview2": "DCC Lite, bu bilgisayarda yüklü olmayan Microsoft Edge WebView2 Runtime gerektirir.\n\nmicrosoft.com/edge/webview2 adresinden yükleyin veya tarayıcıda açılan DCCLite.exe sürümünü kullanın.", "window": "DCC Lite penceresi oluşturulamadı.", "nodevice": "Cihaz bulunamadı.", "disabled": "Bu cihaz devre dışı.", "badaddr": "Kanal adresi kullanılamaz."},
	"ru": {"webview2": "Для DCC Lite требуется Microsoft Edge WebView2 Runtime, который не установлен на этом компьютере.\n\nУстановите его с microsoft.com/edge/webview2 или используйте версию DCCLite.exe, открывающуюся в браузере.", "window": "Не удалось создать окно DCC Lite.", "nodevice": "Устройство не найдено.", "disabled": "Это устройство отключено.", "badaddr": "Адрес канала непригоден."},
	"zh": {"webview2": "DCC Lite 需要 Microsoft Edge WebView2 运行时，但此计算机上未安装。\n\n请从 microsoft.com/edge/webview2 安装，或使用在浏览器中打开的 DCCLite.exe 版本。", "window": "无法创建 DCC Lite 窗口。", "nodevice": "未找到设备。", "disabled": "此设备已被禁用。", "badaddr": "频道地址不可用。"},
}

func msg(key string) string {
	code := strings.ToLower(os.Getenv("DCC_LANG"))
	if code == "" {
		code = osLang()
	}
	if len(code) > 2 {
		code = code[:2]
	}
	if m, ok := nativeMsgs[code]; ok && m[key] != "" {
		return m[key]
	}
	return nativeMsgs["en"][key]
}
