use clap::Parser;

#[derive(Parser)]
#[command(
    name = "qqboxd",
    version,
    about = "Docker-compatible daemon for qqbox sandboxes"
)]
struct Cli {
    #[arg(long, default_value = "127.0.0.1:2375")]
    listen: String,
    #[arg(long)]
    token: Option<String>,
    /// Independent secret for the restore capability; generated from OS
    /// entropy and printed at startup when omitted.
    #[arg(long)]
    migration_secret: Option<String>,
    #[arg(long, default_value = "qqbox")]
    project: String,
    #[arg(long)]
    routes: bool,
}

fn main() {
    let cli = Cli::parse();
    if cli.routes {
        for route in qqbox::daemon::Daemon::routes() {
            println!("{route}");
        }
        return;
    }
    let daemon = qqbox::daemon::Daemon::with_migration_secret(
        cli.token.clone(),
        cli.migration_secret.clone(),
    )
    .unwrap_or_else(|e| {
        eprintln!("qqboxd: {e}");
        std::process::exit(1)
    });
    println!(
        "qqboxd {} project={} listening on {}",
        env!("CARGO_PKG_VERSION"),
        cli.project,
        cli.listen
    );
    println!("bearer token: {}", daemon.token().unwrap_or_default());
    println!("migration capability: {}", daemon.migration_capability());
    if let Err(err) = daemon.serve(&cli.listen) {
        eprintln!("qqboxd: {err}");
        std::process::exit(1);
    }
}
