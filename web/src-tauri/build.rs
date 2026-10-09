fn main() {
    let mut windows = tauri_build::WindowsAttributes::new();
    if let Ok(icon) = std::env::var("NOVATRADER_WIN_ICON") {
        if !icon.is_empty() {
            windows = windows.window_icon_path(icon);
        }
    }
    tauri_build::try_build(tauri_build::Attributes::new().windows_attributes(windows))
        .expect("failed to run tauri build");
}
