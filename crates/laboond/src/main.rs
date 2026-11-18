use clap::Parser;

#[derive(Parser)]
#[command(
    name = "laboond",
    version,
    about = "Docker-compatible daemon for laboon sandboxes"
)]
struct Cli {
    #[arg(long, default_value = "127.0.0.1:2375")]
    listen: String,
    #[arg(long)]
    token: Option<String>,
    #[arg(long, default_value = "laboon")]
    project: String,
    #[arg(long)]
    routes: bool,
}

fn main() {
    let cli = Cli::parse();
    if cli.routes {
        for route in laboon::daemon::Daemon::routes() {
            println!("{route}");
        }
        return;
    }
    let daemon = laboon::daemon::Daemon::new(cli.token.clone());
    println!(
        "laboond {} project={} listening on {}",
        env!("CARGO_PKG_VERSION"),
        cli.project,
        cli.listen
    );
    println!("bearer token: {}", daemon.token().unwrap_or_default());
    if let Err(err) = daemon.serve(&cli.listen) {
        eprintln!("laboond: {err}");
        std::process::exit(1);
    }
}
